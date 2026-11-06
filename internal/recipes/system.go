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

	"github.com/sagnik26/macdoc/internal/fsread"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/safety"
)

func init() {
	Register("app_caches", DetectAppCaches)
	Register("downloads_large_old", DetectDownloadsLargeOld)
	Register("local_snapshots", detectLocalSnapshots)
}

// DetectAppCaches emits kept children of params["dir"] (~/Library/Caches) of
// at least params["min_bytes"], skipping Apple's caches (com.apple.*) and the
// comma-separated names in params["exclude"], which other recipes own.
func DetectAppCaches(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := RequireParam(r, "dir")
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

// DetectDownloadsLargeOld emits kept file nodes directly in params["dir"]
// (~/Downloads) of at least params["min_bytes"] that are older than
// stale_days. params["files"] picks which: "installers" (an installerExts
// extension, case-insensitive) or "other" (everything else).
func DetectDownloadsLargeOld(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := RequireParam(r, "dir")
	if err != nil {
		return nil, err
	}
	files, err := RequireParam(r, "files")
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
			!Stale(env, n.MtimeMax, r.StaleDays) {
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
		return nil, fmt.Errorf("tmutil listlocalsnapshots: exit %d: %s", res.ExitCode, FirstLine(res.Stderr))
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
		Note:  Plural(n, "local snapshot", "local snapshots"),
	}}, nil
}

// Helpers shared by the P3-W2 detectors.

// RequireParam returns params[key] or an error naming the recipe.
func RequireParam(r Recipe, key string) (string, error) {
	v := r.Params[key]
	if v == "" {
		return "", fmt.Errorf("recipe %s: detector %s needs params.%s", r.ID, r.Detector, key)
	}
	return v, nil
}

// Stale reports whether mtime (unix seconds) is at least days before
// env.Now, or the wall clock if env.Now is zero. An unknown mtime (0) is
// never Stale; days <= 0 always is.
func Stale(env Env, mtime int64, days int) bool {
	if days <= 0 {
		return true
	}
	if mtime <= 0 {
		return false
	}
	return env.Clock().Sub(unixTime(mtime)) >= time.Duration(days)*24*time.Hour
}

// BelowHome returns p's components below home, or nil if p isn't strictly
// inside home.
func BelowHome(home, p string) []string {
	rest, ok := strings.CutPrefix(p, strings.TrimSuffix(home, "/")+"/")
	if !ok || rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

// ProjectDirs returns the kept directories below home named name (case-
// insensitive) that are at least two components below home and have no
// ancestor below home with the same name, as patterns like
// "~/*/**/node_modules" require. Tool and app installs are skipped: the first
// component below home must not be hidden (~/.nvm, ~/.cursor, ~/.local, …)
// or Library. Directories in protected folders (~/Documents, ~/Desktop, …)
// are skipped too: CheckTarget would reject them anyway.
func ProjectDirs(env Env, name string) ([]model.Node, error) {
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
		comps := BelowHome(env.Home, n.Path)
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

// maxSmallFile is the default limit of ReadSmallFile.
const maxSmallFile = fsread.MaxSmallFile

// ReadSmallFile errors: anything but a regular file, or over the limit.
var (
	ErrNotRegular = fsread.ErrNotRegular
	ErrTooLarge   = fsread.ErrTooLarge
)

// ReadSmallFile reads a small regular file without following a final
// symlink or blocking on a FIFO (fsread.SmallFile). Every detector file read
// goes through it.
func ReadSmallFile(path string, limit int64) ([]byte, error) {
	return fsread.SmallFile(path, limit)
}

// ErrNoDryRun is returned when a native detector's recipe lacks the dry-run
// argv it runs.
var ErrNoDryRun = errors.New("needs native.dry_run_argv")

// RunDryRun runs the recipe's own dry-run argv through env.Runner.
func RunDryRun(ctx context.Context, env Env, r Recipe, timeout time.Duration) (platform.Result, error) {
	if r.Native == nil || len(r.Native.DryRunArgv) == 0 {
		return platform.Result{}, fmt.Errorf("recipe %s: %w", r.ID, ErrNoDryRun)
	}
	argv := r.Native.DryRunArgv
	// Validate checks this too; refusing here as well means a recipe that
	// skipped validation still can't make a detector mutate anything.
	if !ArgvIn(argv, DryRunArgvAllowed) {
		return platform.Result{}, fmt.Errorf("recipe %s: dry-run argv %q not in the allow-list", r.ID, argv)
	}
	return env.Runner.Run(ctx, platform.Command{Name: argv[0], Args: argv[1:], Timeout: timeout})
}

// MaxPlausibleBytes (1 PB) caps any size a detector reports. A larger value
// from a tool or a sum is a parse error or overflow, never a real size.
const MaxPlausibleBytes int64 = 1_000_000_000_000_000

// AddSize returns total+b, or false if b is negative or the sum would pass
// MaxPlausibleBytes (which also rules out int64 overflow).
func AddSize(total, b int64) (int64, bool) {
	if b < 0 || total < 0 || b > MaxPlausibleBytes-total {
		return 0, false
	}
	return total + b, true
}

// implausibleSize is the size of a sum that overflowed or passed
// MaxPlausibleBytes.
func implausibleSize(what string) model.Bytes {
	return model.Bytes{Source: model.SourceUnknown, Reason: what + " reported an implausibly large size"}
}

// ParseSize parses a human size like "377MB", "1.1GB (45%)", "4.7 GB",
// "199.2KB", "12kB" or "0B". base is 1000 (Docker, Ollama) or 1024
// (Homebrew, conda).
func ParseSize(s string, base float64) (int64, bool) {
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
	if math.IsNaN(v) || math.IsInf(v, 0) || v >= math.MaxInt64 || v > float64(MaxPlausibleBytes) {
		return 0, false
	}
	return int64(v + 0.5), true
}

// FirstLine returns the first non-empty line of b, for error messages.
func FirstLine(b []byte) string {
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// Plural formats n with one or many, for example "1 file" or "3 files".
func Plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
