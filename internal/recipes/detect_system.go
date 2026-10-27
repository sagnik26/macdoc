package recipes

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"macdoc/internal/fsread"
	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/safety"
)

func init() {
	Register("app_caches", detectAppCaches)
	Register("downloads_large_old", detectDownloadsLargeOld)
	Register("local_snapshots", detectLocalSnapshots)
}

// detectAppCaches emits kept children of params["dir"] (~/Library/Caches) of
// at least params["min_bytes"], skipping Apple's caches (com.apple.*) and the
// comma-separated names in params["exclude"], which other recipes own.
func detectAppCaches(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := requireParam(r, "dir")
	if err != nil || env.Nodes == nil {
		return nil, err
	}
	minBytes, err := paramInt(r, "min_bytes", 1)
	if err != nil {
		return nil, err
	}
	exclude := paramSet(r, "exclude")
	dir = ExpandHome(env.Home, dir)
	nodes, err := env.Nodes.Nodes(dir, 1)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range nodes {
		name := strings.ToLower(filepath.Base(n.Path))
		if n.Path == dir || n.Kind == model.NodeFile || n.Bytes < minBytes ||
			strings.HasPrefix(name, "com.apple.") || exclude[name] {
			continue
		}
		f := nodeFinding(n)
		f.Title = filepath.Base(n.Path) + " cache"
		out = append(out, f)
	}
	return out, nil
}

// installerExts are the extensions (lowercase) of installers and archives,
// which can be downloaded again. Any other large download may be the only
// copy (an AirDrop or a phone import, for example).
var installerExts = []string{
	".dmg", ".pkg", ".mpkg", ".xip", ".zip", ".iso", ".tar", ".tgz", ".gz",
	".bz2", ".xz", ".7z", ".rar", ".ipa", ".apk", ".aab",
}

// detectDownloadsLargeOld emits kept file nodes directly in params["dir"]
// (~/Downloads) of at least params["min_bytes"] that are older than
// stale_days. params["files"] picks which: "installers" (an installerExts
// extension, case-insensitive) or "other" (everything else).
func detectDownloadsLargeOld(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := requireParam(r, "dir")
	if err != nil {
		return nil, err
	}
	files, err := requireParam(r, "files")
	if err != nil {
		return nil, err
	}
	if files != "installers" && files != "other" {
		return nil, fmt.Errorf("recipe %s: params.files must be installers or other", r.ID)
	}
	if env.Nodes == nil {
		return nil, nil
	}
	minBytes, err := paramInt(r, "min_bytes", 1)
	if err != nil {
		return nil, err
	}
	dir = ExpandHome(env.Home, dir)
	nodes, err := env.Nodes.Nodes(dir, 1)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range nodes {
		if n.Kind != model.NodeFile || filepath.Dir(n.Path) != dir || n.Bytes < minBytes ||
			!stale(env, n.MtimeMax, r.StaleDays) {
			continue
		}
		installer := slices.Contains(installerExts, strings.ToLower(filepath.Ext(n.Path)))
		if installer != (files == "installers") {
			continue
		}
		out = append(out, nodeFinding(n))
	}
	return out, nil
}

// detectLocalSnapshots counts `tmutil listlocalsnapshots /` entries. macOS
// doesn't report snapshot sizes, so Bytes is unknown.
func detectLocalSnapshots(ctx context.Context, env Env, _ Recipe) ([]Finding, error) {
	if env.Runner == nil || !ToolInstalled("tmutil") {
		return nil, nil
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: "tmutil", Args: []string{"listlocalsnapshots", "/"}})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("tmutil listlocalsnapshots: exit %d: %s", res.ExitCode, firstLine(res.Stderr))
	}
	n := 0
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "com.apple.") {
			n++
		}
	}
	if n == 0 {
		return nil, nil
	}
	return []Finding{{
		Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "macOS doesn't report local snapshot sizes"},
		Note:  plural(n, "local snapshot", "local snapshots"),
	}}, nil
}

// Helpers shared by the P3-W2 detectors.

// requireParam returns params[key] or an error naming the recipe.
func requireParam(r Recipe, key string) (string, error) {
	v := r.Params[key]
	if v == "" {
		return "", fmt.Errorf("recipe %s: detector %s needs params.%s", r.ID, r.Detector, key)
	}
	return v, nil
}

// stale reports whether mtime (unix seconds) is at least days before
// env.Now, or the wall clock if env.Now is zero. An unknown mtime (0) is
// never stale; days <= 0 always is.
func stale(env Env, mtime int64, days int) bool {
	if days <= 0 {
		return true
	}
	if mtime <= 0 {
		return false
	}
	return env.now().Sub(unixTime(mtime)) >= time.Duration(days)*24*time.Hour
}

// belowHome returns p's components below home, or nil if p isn't strictly
// inside home.
func belowHome(home, p string) []string {
	rest, ok := strings.CutPrefix(p, strings.TrimSuffix(home, "/")+"/")
	if !ok || rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

// projectDirs returns the kept directories below home named name (case-
// insensitive) that are at least two components below home and have no
// ancestor below home with the same name, as patterns like
// "~/*/**/node_modules" require. Tool and app installs are skipped: the first
// component below home must not be hidden (~/.nvm, ~/.cursor, ~/.local, …)
// or Library. Directories in protected folders (~/Documents, ~/Desktop, …)
// are skipped too: CheckTarget would reject them anyway.
func projectDirs(env Env, name string) ([]model.Node, error) {
	nodes, err := env.Nodes.Nodes(env.Home, 0)
	if err != nil {
		return nil, err
	}
	pol, err := safety.NewPolicy(env.Home, nil)
	if err != nil {
		return nil, err
	}
	var out []model.Node
	for _, n := range nodes {
		comps := belowHome(env.Home, n.Path)
		if n.Kind == model.NodeFile || len(comps) < 2 || !strings.EqualFold(comps[len(comps)-1], name) ||
			strings.HasPrefix(comps[0], ".") || strings.EqualFold(comps[0], "Library") {
			continue
		}
		// IsProtected is lexical and needs the canonical path.
		c, err := safety.Canonicalize(n.Path)
		if err != nil {
			continue
		}
		if prot, _ := pol.IsProtected(c); prot {
			continue
		}
		nested := false
		for _, c := range comps[:len(comps)-1] {
			nested = nested || strings.EqualFold(c, name)
		}
		if !nested {
			out = append(out, n)
		}
	}
	return out, nil
}

// maxSmallFile is the default limit of readSmallFile.
const maxSmallFile = fsread.MaxSmallFile

// readSmallFile errors: anything but a regular file, or over the limit.
var (
	errNotRegular = fsread.ErrNotRegular
	errTooLarge   = fsread.ErrTooLarge
)

// readSmallFile reads a small regular file without following a final
// symlink or blocking on a FIFO (fsread.SmallFile). Every detector file read
// goes through it.
func readSmallFile(path string, limit int64) ([]byte, error) {
	return fsread.SmallFile(path, limit)
}

// errNoDryRun is returned when a native detector's recipe lacks the dry-run
// argv it runs.
var errNoDryRun = errors.New("needs native.dry_run_argv")

// runDryRun runs the recipe's own dry-run argv through env.Runner.
func runDryRun(ctx context.Context, env Env, r Recipe, timeout time.Duration) (platform.Result, error) {
	if r.Native == nil || len(r.Native.DryRunArgv) == 0 {
		return platform.Result{}, fmt.Errorf("recipe %s: %w", r.ID, errNoDryRun)
	}
	argv := r.Native.DryRunArgv
	// Validate checks this too; refusing here as well means a recipe that
	// skipped validation still can't make a detector mutate anything.
	if !argvIn(argv, dryRunArgvAllowed) {
		return platform.Result{}, fmt.Errorf("recipe %s: dry-run argv %q not in the allow-list", r.ID, argv)
	}
	return env.Runner.Run(ctx, platform.Command{Name: argv[0], Args: argv[1:], Timeout: timeout})
}

// maxPlausibleBytes (1 PB) caps any size a detector reports. A larger value
// from a tool or a sum is a parse error or overflow, never a real size.
const maxPlausibleBytes int64 = 1_000_000_000_000_000

// addSize returns total+b, or false if b is negative or the sum would pass
// maxPlausibleBytes (which also rules out int64 overflow).
func addSize(total, b int64) (int64, bool) {
	if b < 0 || total < 0 || b > maxPlausibleBytes-total {
		return 0, false
	}
	return total + b, true
}

// implausibleSize is the size of a sum that overflowed or passed
// maxPlausibleBytes.
func implausibleSize(what string) model.Bytes {
	return model.Bytes{Source: model.SourceUnknown, Reason: what + " reported an implausibly large size"}
}

// parseSize parses a human size like "377MB", "1.1GB (45%)", "4.7 GB",
// "199.2KB", "12kB" or "0B". base is 1000 (Docker, Ollama) or 1024
// (Homebrew, conda).
func parseSize(s string, base float64) (int64, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.ReplaceAll(s, " ", "")
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.' || s[i] == ',') {
		i++
	}
	num, unit := strings.ReplaceAll(s[:i], ",", ""), strings.ToUpper(s[i:])
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	exp := map[string]int{"B": 0, "": 0, "KB": 1, "K": 1, "KIB": 1, "MB": 2, "M": 2, "MIB": 2,
		"GB": 3, "G": 3, "GIB": 3, "TB": 4, "T": 4, "TIB": 4}
	e, ok := exp[unit]
	if !ok {
		return 0, false
	}
	for range e {
		v *= base
	}
	// Reject NaN, infinities and anything over 1 PB before converting:
	// float64 to int64 is implementation-defined out of range.
	if math.IsNaN(v) || math.IsInf(v, 0) || v >= math.MaxInt64 || v > float64(maxPlausibleBytes) {
		return 0, false
	}
	return int64(v + 0.5), true
}

// firstLine returns the first non-empty line of b, for error messages.
func firstLine(b []byte) string {
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
