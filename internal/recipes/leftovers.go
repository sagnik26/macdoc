package recipes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sagnik26/macdoc/internal/inventory"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

func init() { Register("app_leftovers", DetectAppLeftovers) }

// savedStateSuffix is added to an app's bundle identifier for its saved window
// state folder.
const savedStateSuffix = ".savedstate"

// DefaultAppDirs are the folders apps are looked for in.
func DefaultAppDirs(home string) []string {
	return []string{"/Applications", "/System/Applications", "/System/Applications/Utilities", filepath.Join(home, "Applications")}
}

// launchDirs hold the property lists of background items. A name here that
// matches a folder keeps that folder from being called a leftover.
func launchDirs(home string) []string {
	return []string{filepath.Join(home, "Library", "LaunchAgents"), "/Library/LaunchAgents", "/Library/LaunchDaemons"}
}

// LeftoverID returns the bundle identifier a folder in a Library data folder
// is named after, or false when its name is not one. Only reverse-DNS names
// count (three or more parts such as com.vendor.app, optionally with the
// ".savedState" suffix): a name like "Slack" could be anything, so it is never
// treated as an app's data. Apple's own identifiers are never returned.
func LeftoverID(name string) (string, bool) {
	id := name
	if strings.HasSuffix(strings.ToLower(id), savedStateSuffix) {
		id = id[:len(id)-len(savedStateSuffix)]
	}
	parts := strings.Split(id, ".")
	if len(parts) < 3 || strings.EqualFold(parts[0], "com") && strings.EqualFold(parts[1], "apple") {
		return "", false
	}
	for _, p := range parts {
		if p == "" || strings.IndexFunc(p, notIDChar) >= 0 {
			return "", false
		}
	}
	return id, true
}

// notIDChar reports whether r cannot be part of a bundle identifier.
func notIDChar(r rune) bool {
	letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	return !letter && (r < '0' || r > '9') && r != '-' && r != '_'
}

// InstalledIDs returns, in lower case, the bundle identifier of every app in
// appDirs and the name of every background item property list. It fails when
// no app could be read, so a caller can never treat an unreadable machine as
// one with nothing installed.
func InstalledIDs(ctx context.Context, r platform.Runner, home string, appDirs []string) (map[string]bool, error) {
	if appDirs == nil {
		appDirs = DefaultAppDirs(home)
	}
	ids, err := inventory.AppBundleIDs(ctx, inventory.Env{Home: home, AppDirs: appDirs, Runner: r})
	if err != nil {
		return nil, fmt.Errorf("installed apps: %w", err)
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[strings.ToLower(id)] = true
	}
	for _, dir := range launchDirs(home) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if stem, ok := strings.CutSuffix(strings.ToLower(e.Name()), ".plist"); ok {
				set[stem] = true
			}
		}
	}
	return set, nil
}

// OwnedByInstalled reports whether id belongs to something installed: it is
// the same as an installed identifier, or one is the other plus ".something"
// (a helper or extension of the app). Case is ignored.
func OwnedByInstalled(id string, installed map[string]bool) bool {
	id = strings.ToLower(id)
	for s := range installed {
		if id == s || strings.HasPrefix(id, s+".") || strings.HasPrefix(s, id+".") {
			return true
		}
	}
	return false
}

// DetectAppLeftovers emits the kept children of each folder in
// params["dirs"] (comma-separated, "~" allowed) that are named after an app's
// bundle identifier when no installed app or background item matches, of at
// least params["min_bytes"], and unchanged for stale_days. Names that are not
// a bundle identifier, Apple's identifiers, anything a helper or background
// item of an installed app could own, and recent folders are skipped. If the
// installed apps can't be listed it reports an error and offers nothing.
func DetectAppLeftovers(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	dirsParam, err := RequireParam(r, "dirs")
	if err != nil || env.Nodes == nil {
		return nil, err
	}
	minBytes, err := paramInt(r, "min_bytes", 1)
	if err != nil {
		return nil, err
	}
	installed, err := InstalledIDs(ctx, env.Runner, env.Home, env.AppDirs)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, d := range strings.Split(dirsParam, ",") {
		dir := ExpandHome(env.Home, strings.TrimSpace(d))
		nodes, err := env.Nodes.Nodes(dir, 1)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			id, ok := LeftoverID(filepath.Base(n.Path))
			if !ok || filepath.Dir(n.Path) != dir || n.Bytes < minBytes || !Stale(env, n.MtimeMax, r.StaleDays) ||
				OwnedByInstalled(id, installed) {
				continue
			}
			f := nodeFinding(n)
			f.Title = fmt.Sprintf("Data left by %s (%s)", id, filepath.Base(dir))
			f.Note = "No installed app or background item has this identifier."
			out = append(out, f)
		}
	}
	return out, nil
}

func init() {
	Register("user_app_bundles", DetectUserAppBundles)
	Register("system_app_bundles", DetectUserAppBundles)
}

// DetectUserAppBundles emits each app bundle directly in params["dir"]
// (~/Applications, or /Applications for the system_app_bundles recipe, whose
// allow pattern safety.AppsPattern is the one exception to the protected
// roots, ADR 0011). Apple's apps (com.apple.*) and anything that is not a
// bundle folder are skipped. The size comes from the scan when it kept the
// bundle (100 MB and up, or through keep_children_of) and is unknown
// otherwise, never zero.
func DetectUserAppBundles(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	dirParam, err := RequireParam(r, "dir")
	if err != nil {
		return nil, err
	}
	dir := ExpandHome(env.Home, dirParam)
	if dir == "/Applications" && env.AppsFolder != "" {
		dir = env.AppsFolder
	}
	apps, err := inventory.AppsIn(ctx, inventory.Env{Home: env.Home, AppDirs: []string{dir}, Runner: env.Runner})
	if err != nil {
		return nil, err
	}
	sizes := map[string]model.Node{}
	if env.Nodes != nil {
		if nodes, err := env.Nodes.Nodes(dir, 1); err == nil {
			for _, n := range nodes {
				sizes[n.Path] = n
			}
		}
	}
	var out []Finding
	for _, a := range apps {
		if strings.HasPrefix(strings.ToLower(a.BundleID), "com.apple.") {
			continue
		}
		bundle := bundlePath(a.Evidence)
		if filepath.Dir(bundle) != dir || !removable(bundle) {
			continue
		}
		f := Finding{Target: bundle, Title: a.Name + " (app)",
			Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "the scan didn't measure this app"}}
		if n, ok := sizes[bundle]; ok {
			f = nodeFinding(n)
			f.Title = a.Name + " (app)"
		}
		f.Note = "Moves the app out of ~/Applications. Its settings and caches stay until you remove them."
		out = append(out, f)
	}
	return out, nil
}

// removable reports whether macdoc may offer a bundle: not a Mac App Store
// app (those are managed by the store and can't be changed), and one the
// current user can actually move (they own it, or it is group-writable for
// the admin group). A bundle that fails either test is left out, so the
// plan never promises what the system would refuse.
func removable(bundle string) bool {
	if _, err := os.Lstat(filepath.Join(bundle, "Contents", "_MASReceipt")); err == nil {
		return false
	}
	fi, err := os.Lstat(bundle)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Getuid() || fi.Mode().Perm()&0o020 != 0
}

// bundlePath is the .app folder named by an inventory evidence string, which
// is the path, optionally followed by a note in parentheses.
func bundlePath(evidence string) string {
	if i := strings.Index(evidence, ".app"); i >= 0 {
		return evidence[:i+len(".app")]
	}
	return evidence
}
