package recipes

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/platform"
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
	exclude := map[string]bool{}
	for _, n := range strings.Split(r.Params["exclude"], ",") {
		if n = strings.TrimSpace(n); n != "" {
			exclude[strings.ToLower(n)] = true
		}
	}
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

// detectDownloadsLargeOld emits kept file nodes directly in params["dir"]
// (~/Downloads) of at least params["min_bytes"] that are older than
// stale_days.
func detectDownloadsLargeOld(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := requireParam(r, "dir")
	if err != nil || env.Nodes == nil {
		return nil, err
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
// env.Now. An unknown mtime (0) is never stale; days <= 0 always is.
func stale(env Env, mtime int64, days int) bool {
	if days <= 0 {
		return true
	}
	if mtime <= 0 {
		return false
	}
	return env.Now.Sub(unixTime(mtime)) >= time.Duration(days)*24*time.Hour
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
// or Library.
func projectDirs(env Env, name string) ([]model.Node, error) {
	nodes, err := env.Nodes.Nodes(env.Home, 0)
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

// errNoDryRun is returned when a native detector's recipe lacks the dry-run
// argv it runs.
var errNoDryRun = errors.New("needs native.dry_run_argv")

// runDryRun runs the recipe's own dry-run argv through env.Runner.
func runDryRun(ctx context.Context, env Env, r Recipe, timeout time.Duration) (platform.Result, error) {
	if r.Native == nil || len(r.Native.DryRunArgv) == 0 {
		return platform.Result{}, fmt.Errorf("recipe %s: %w", r.ID, errNoDryRun)
	}
	argv := r.Native.DryRunArgv
	return env.Runner.Run(ctx, platform.Command{Name: argv[0], Args: argv[1:], Timeout: timeout})
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
