package recipes

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/safety"
)

// The detectors of the system locations `reclaim` may clean with
// administrator rights (ADR 0014 §1). They only read. Each finding's target
// must still pass safety.Policy.CheckSystemTarget for the recipe's kind
// before it becomes a suggestion, so a detector can't widen the list.
func init() {
	Register("system_dir", DetectSystemDir)
	Register("system_launchd", DetectSystemLaunchd)
	Register("system_helpers", DetectSystemHelpers)
	Register("system_backups", DetectSystemBackups)
}

const (
	// systemItemLimit bounds how many items one folder gives.
	systemItemLimit = 300
	// systemWalkLimit bounds how many entries one item's size walk reads.
	systemWalkLimit = 200000
	systemNote      = "Needs your password, and can't be restored."
)

// SystemKindOf returns the system kind a recipe cleans (its params.kind), or
// "" when it is not one of safety.SystemKinds.
func SystemKindOf(r Recipe) string {
	for _, k := range safety.SystemKinds {
		if r.Params["kind"] == k {
			return k
		}
	}
	return ""
}

// DetectSystemDir emits one finding per item directly in params["dir"]
// (/Library/Caches, /Library/Logs, /private/var/log), sized by a bounded walk.
// A symlink and an Apple item (com.apple.*) are skipped. If the walk can't
// read everything (root-only files) the size is unknown, never a guess.
func DetectSystemDir(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := RequireParam(r, "dir")
	if err != nil || !filepath.IsAbs(dir) {
		return nil, err
	}
	dir = filepath.Join(env.SystemBase, dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil //nolint:nilerr // an unreadable folder gives no suggestion
	}
	var out []Finding
	for i, e := range entries {
		name := e.Name()
		if i >= systemItemLimit || e.Type()&fs.ModeSymlink != 0 || strings.HasPrefix(strings.ToLower(name), applePrefixLower) {
			continue
		}
		path := filepath.Join(dir, name)
		bytes, newest, complete := measureTree(ctx, path)
		f := Finding{Target: path, Note: systemNote}
		if complete {
			f.Bytes = model.Bytes{Value: bytes, Source: model.SourceMeasured}
		} else {
			f.Bytes = model.Bytes{Source: model.SourceUnknown, Reason: "some of it can only be read with administrator rights"}
		}
		if newest > 0 {
			f.LastUsed = unixTime(newest)
		}
		out = append(out, f)
	}
	return out, ctx.Err()
}

const applePrefixLower = "com.apple."

// measureTree sums the allocated bytes under path and finds the newest
// modification time. complete is false if anything couldn't be read or the
// walk hit its limit.
func measureTree(ctx context.Context, path string) (bytes int64, newest int64, complete bool) {
	complete = true
	n := 0
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			complete = false
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if n++; n > systemWalkLimit || ctx.Err() != nil {
			complete = false
			return errors.New("limit")
		}
		fi, err := d.Info()
		if err != nil {
			complete = false
			return nil
		}
		if t := fi.ModTime().Unix(); t > newest {
			newest = t
		}
		if fi.Mode().IsRegular() {
			bytes += allocated(fi)
		}
		return nil
	})
	return bytes, newest, complete
}

// DetectSystemLaunchd emits the background items in params["dir"]
// (/Library/LaunchDaemons) whose program no longer exists, by the same test as
// the user-level launch agents.
func DetectSystemLaunchd(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	one := r
	one.Params = map[string]string{"dir": filepath.Join(env.SystemBase, r.Params["dir"])}
	fs, err := DetectBrokenLaunchAgents(ctx, env, one)
	for i := range fs {
		fs[i].Note += " " + systemNote
	}
	return fs, err
}

// DetectSystemHelpers emits the files in /Library/PrivilegedHelperTools named
// after a bundle identifier that no installed app, helper or background item
// owns. If the installed apps can't be listed it offers nothing.
func DetectSystemHelpers(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := RequireParam(r, "dir")
	if err != nil || env.Runner == nil || !filepath.IsAbs(dir) {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(env.SystemBase, dir))
	if err != nil {
		return nil, nil //nolint:nilerr // no folder means no helpers
	}
	installed, err := InstalledIDs(ctx, env.Runner, env.Home, env.AppDirs)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for i, e := range entries {
		id, ok := LeftoverID(e.Name())
		if i >= systemItemLimit || !ok || e.IsDir() || e.Type()&fs.ModeSymlink != 0 || OwnedByInstalled(id, installed) {
			continue
		}
		path := filepath.Join(env.SystemBase, dir, e.Name())
		f := Finding{Target: path, Title: id + " (helper tool of an app that's gone)",
			Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "a helper tool's size isn't measured"},
			Note:  "No installed app or background item has this identifier. " + systemNote}
		if fi, err := os.Lstat(path); err == nil {
			f.Bytes = model.Bytes{Value: allocated(fi), Source: model.SourceMeasured}
		}
		out = append(out, f)
	}
	return out, nil
}

// DetectSystemBackups emits each interrupted Time Machine backup (a folder
// ending .inProgress one level below a machine folder of a destination's
// Backups.backupdb), found through `tmutil destinationinfo` and folder
// listings only. Time Machine doesn't report a backup's size, so it is unknown.
func DetectSystemBackups(ctx context.Context, env Env, _ Recipe) ([]Finding, error) {
	if env.Runner == nil || !ToolInstalled("tmutil") {
		return nil, nil
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: "tmutil", Args: []string{"destinationinfo"}})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(string(res.Stderr), "No destinations") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmutil destinationinfo: exit %d: %s", res.ExitCode, FirstLine(res.Stderr))
	}
	var out []Finding
	for _, mount := range destinationMounts(string(res.Stdout)) {
		for _, p := range inProgressPaths(filepath.Join(mount, "Backups.backupdb")) {
			out = append(out, Finding{Target: p, Title: "Interrupted backup " + filepath.Base(p),
				Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "Time Machine doesn't report the size of an interrupted backup"},
				Note:  systemNote})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out, nil
}

// inProgressPaths lists the ".inProgress" folders one level below each machine
// folder of a Backups.backupdb. An unreadable folder gives none.
func inProgressPaths(backupdb string) []string {
	machines, err := os.ReadDir(backupdb)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range machines {
		if !m.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(backupdb, m.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasSuffix(e.Name(), ".inProgress") {
				out = append(out, filepath.Join(backupdb, m.Name(), e.Name()))
			}
		}
	}
	return out
}

// SystemArgv is the command that removes one system target with
// administrator rights (ADR 0014 §2): a fixed program and fixed flags, then
// the path. It is built from the target and kind, never taken from a plan.
func SystemArgv(kind, path string) []string {
	switch kind {
	case safety.SysBackups:
		return []string{"tmutil", "delete", path}
	case safety.SysLaunchd, safety.SysHelpers:
		return []string{"rm", "-f", "--", path}
	}
	return []string{"rm", "-rf", "--", path}
}

// SystemArgvOK reports whether argv is exactly what SystemArgv builds for some
// kind and a path shaped like that kind's targets. The privileged runner
// checks it again, so nothing else can run as administrator through this path.
func SystemArgvOK(argv []string) bool {
	if len(argv) < 3 {
		return false
	}
	path := argv[len(argv)-1]
	for _, k := range safety.SystemKinds {
		if want := SystemArgv(k, path); equalArgv(want, argv) && safety.SystemShapeOK(path, k) {
			return true
		}
	}
	return false
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
