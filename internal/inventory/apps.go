package inventory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagnik26/macdoc/internal/platform"
)

func init() { Register(appsSource{}) }

// appsSource lists the .app bundles in the app folders. Names and bundle
// identifiers come from each bundle's Info.plist, read as JSON by plutil.
type appsSource struct{}

// Name implements Source.
func (appsSource) Name() string { return "apps" }

// Discover implements Source. A missing app folder is not an error.
func (appsSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	var out []Software
	for _, dir := range env.AppDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			if err := ctx.Err(); err != nil {
				return out, err
			}
			out = append(out, appFrom(ctx, env, filepath.Join(dir, e.Name())))
		}
	}
	return out, nil
}

// appFrom reads one bundle. An unreadable Info.plist still gives the app,
// named after its folder, with the evidence saying so.
func appFrom(ctx context.Context, env Env, bundle string) Software {
	base := strings.TrimSuffix(filepath.Base(bundle), ".app")
	sw := Software{Name: base, Kind: "app", Source: "apps", Evidence: bundle}
	if fi, err := os.Stat(bundle); err == nil {
		sw.Installed = fi.ModTime()
	}
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	info, err := plistAsJSON(ctx, env, plist)
	if err != nil {
		sw.Evidence = bundle + " (no readable Info.plist)"
	} else {
		if n := firstString(info, "CFBundleDisplayName", "CFBundleName"); n != "" {
			sw.Name = n
		}
		sw.BundleID = firstString(info, "CFBundleIdentifier")
		sw.Version = firstString(info, "CFBundleShortVersionString")
		if exe := firstString(info, "CFBundleExecutable"); exe != "" && filepath.Base(exe) == exe {
			sw.Slim = slimOf(ctx, env, filepath.Join(bundle, "Contents", "MacOS", exe))
		}
	}
	if sw.BundleID != "" {
		sw.ID = "app:" + sw.BundleID
	} else {
		sw.ID = "app-name:" + sw.Name
	}
	return sw
}

// plistAsJSON runs plutil, which is installed with macOS, and decodes the
// result. It fails when the tool is missing or the file isn't a plist.
func plistAsJSON(ctx context.Context, env Env, path string) (map[string]any, error) {
	if env.Runner == nil {
		return nil, errAbsent{"plutil"}
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: "plutil", Args: []string{"-convert", "json", "-o", "-", path}})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, errAbsent{"readable plist"}
	}
	var m map[string]any
	if err := json.Unmarshal(res.Stdout, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// firstString returns the first non-empty string value among keys.
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// AppBundleIDs returns the bundle identifier of every app in env.AppDirs, one
// entry per app that has one. It fails when no app could be read at all, so a
// caller never mistakes "could not look" for "nothing is installed".
func AppBundleIDs(ctx context.Context, env Env) ([]string, error) {
	apps, err := appsSource{}.Discover(ctx, env)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, a := range apps {
		if a.BundleID != "" {
			ids = append(ids, a.BundleID)
		}
	}
	if len(ids) == 0 {
		return nil, errAbsent{"any readable app"}
	}
	return ids, nil
}

// AppsIn lists the apps in env.AppDirs with their names and bundle
// identifiers. A missing folder is not an error.
func AppsIn(ctx context.Context, env Env) ([]Software, error) {
	return appsSource{}.Discover(ctx, env)
}
