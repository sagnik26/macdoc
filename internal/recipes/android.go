package recipes

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/pathfold"
	"github.com/sagnik26/macdoc/internal/safety"
)

// Detectors for Gradle, the Android SDK, emulators and Android Studio
// (catalog/android.yaml). Read-only: scan nodes, directory listings and
// small text files (*.ini). SDK packages are found on disk rather than with
// sdkmanager, which may point at a different SDK root.

func init() {
	Register("gradle_wrapper_old", detectGradleWrapperOld)
	Register("android_avds", DetectAndroidAVDs)
	RegisterLister("android_avds", listAVDNames)
	Register("android_avd_snapshots", detectAVDSnapshots)
	Register("android_system_images", detectSystemImages)
	Register("android_old_versions", DetectAndroidOldVersions)
	Register("android_studio_old", detectAndroidStudioOld)
}

// AndroidSDKRoot returns $ANDROID_HOME, else $ANDROID_SDK_ROOT, else
// ~/Library/Android/sdk. ok is false when the root isn't strictly inside
// home: SDK packages are only ever quarantined from home.
func AndroidSDKRoot(env Env) (root string, ok bool) {
	root = os.Getenv("ANDROID_HOME")
	if root == "" {
		root = os.Getenv("ANDROID_SDK_ROOT")
	}
	if root == "" {
		root = filepath.Join(env.Home, "Library/Android/sdk")
	}
	root = filepath.Clean(ExpandHome(env.Home, root))
	return root, filepath.IsAbs(root) && underHome(env.Home, root)
}

// underHome reports whether p is strictly below home (case-insensitive, like APFS).
func underHome(home, p string) bool {
	return strings.HasPrefix(pathfold.Fold(p), pathfold.Fold(filepath.Clean(home))+"/")
}

// AvdHome returns where the emulator keeps AVDs: $ANDROID_AVD_HOME, else
// $ANDROID_EMULATOR_HOME/avd, else ~/.android/avd.
func AvdHome(env Env) string {
	if d := os.Getenv("ANDROID_AVD_HOME"); d != "" {
		return filepath.Clean(ExpandHome(env.Home, d))
	}
	if d := os.Getenv("ANDROID_EMULATOR_HOME"); d != "" {
		return filepath.Join(ExpandHome(env.Home, d), "avd")
	}
	return filepath.Join(env.Home, ".android/avd")
}

// readIni parses key=value lines of a small regular file (ReadSmallFile).
// Lines without "=" are ignored.
func readIni(path string) (map[string]string, error) {
	data, err := ReadSmallFile(path, maxSmallFile)
	if err != nil {
		return nil, err
	}
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return kv, sc.Err()
}

// AVD is one emulator from <avd home>/<name>.ini.
type AVD struct {
	name string
	dir  string            // the <name>.avd data directory
	cfg  map[string]string // its config.ini; nil if it has none
	// ok means dir is safe to act on: avdmanager deletes the directory the
	// registry names, so only a real "*.avd" directory strictly inside the
	// AVD home, holding a regular config.ini and not protected, qualifies.
	ok bool
}

// ListAVDs reads the AVD registry. A missing AVD home means no AVDs; any
// other read error is returned, so callers never treat an unreadable
// registry as "no AVDs reference this". Every AVD is returned, so all their
// system images count as used; only those with ok set may become findings.
func ListAVDs(env Env) ([]AVD, error) {
	dir := AvdHome(env)
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pol, err := safety.NewPolicy(env.Home, nil)
	if err != nil {
		return nil, err
	}
	// The AVD home with every symlink resolved, as Canonicalize resolves
	// the parents of each AVD directory.
	c, err := safety.Canonicalize(filepath.Join(dir, "x"))
	if err != nil {
		return nil, err
	}
	canonHome := filepath.Dir(c)
	var out []AVD
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".ini")
		if !ok || !e.Type().IsRegular() || name == "" {
			continue
		}
		ini, err := readIni(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		a := AVD{name: name, dir: filepath.Join(dir, name+".avd")}
		if p := ini["path"]; filepath.IsAbs(p) {
			a.dir = filepath.Clean(p)
		}
		a.cfg, err = readIni(filepath.Join(a.dir, "config.ini"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		a.ok = a.cfg != nil && avdDirOK(pol, canonHome, a.dir)
		out = append(out, a)
	}
	return out, nil
}

// avdDirOK reports whether dir canonicalizes strictly inside the canonical
// AVD home, is named "*.avd", is a real directory (not a symlink) and is not
// protected.
func avdDirOK(pol *safety.Policy, canonHome, dir string) bool {
	c, err := safety.Canonicalize(dir)
	if err != nil || !strings.HasSuffix(strings.ToLower(filepath.Base(c)), ".avd") ||
		!safety.Within(canonHome, c) || safety.Within(c, canonHome) {
		return false
	}
	if fi, err := os.Lstat(c); err != nil || !fi.IsDir() {
		return false
	}
	prot, _ := pol.IsProtected(c)
	return !prot
}

// subdirs lists the real directories (not symlinks) in dir, sorted. A missing
// dir gives none.
func subdirs(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// dirFinding sizes a directory found on disk from the scan; if the scan
// didn't keep it the size is unknown, never 0.
func dirFinding(env Env, path, title string) (Finding, error) {
	n, found, err := ScanNode(env, path)
	if err != nil {
		return Finding{}, err
	}
	if !found {
		return Finding{Target: path, Title: title, Bytes: unscanned()}, nil
	}
	f := nodeFinding(n)
	f.Title = title
	return f, nil
}

// ---- gradle.wrapper_dists_old ----

var gradleDistRE = regexp.MustCompile(`^gradle-(\d+(?:\.\d+)*)-(?:bin|all)$`)

func gradleDistVersion(n model.Node) []int {
	m := gradleDistRE.FindStringSubmatch(filepath.Base(n.Path))
	if m == nil {
		return nil
	}
	return ParseVersion(m[1])
}

func detectGradleWrapperOld(_ context.Context, env Env, _ Recipe) ([]Finding, error) {
	if env.Nodes == nil {
		return nil, nil
	}
	kids, err := scanChildren(env, filepath.Join(env.Home, ".gradle/wrapper/dists"))
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range allButNewest(kids, gradleDistVersion) {
		f := nodeFinding(n)
		f.Title = "Gradle wrapper " + strings.TrimPrefix(filepath.Base(n.Path), "gradle-")
		out = append(out, f)
	}
	return out, nil
}

// ---- android.avd / android.avd_snapshots ----

func avdTitle(a AVD) string {
	if d := a.cfg["avd.ini.displayname"]; d != "" {
		return d
	}
	return a.name
}

// listAVDNames lists the AVD names that `avdmanager delete avd -n` takes
// (P6-R1 F8). Read-only, and no staleness: the plan already decided that.
func listAVDNames(_ context.Context, env Env, _ Recipe) ([]string, error) {
	if !ToolInstalled("avdmanager") {
		return nil, nil
	}
	avds, err := ListAVDs(env)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range avds {
		if a.ok && ValidNativeArg(a.name) {
			out = append(out, a.name)
		}
	}
	return out, nil
}

// DetectAndroidAVDs emits emulators idle for r.StaleDays (default 60). The
// AVD name is the native argument for `avdmanager delete avd -n`.
func DetectAndroidAVDs(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Nodes == nil || !ToolInstalled("avdmanager") {
		return nil, nil
	}
	avds, err := ListAVDs(env)
	if err != nil {
		return nil, err
	}
	days := r.StaleDays
	if days <= 0 {
		days = 60
	}
	var out []Finding
	for _, a := range avds {
		if !a.ok || !ValidNativeArg(a.name) {
			continue
		}
		n, found, err := ScanNode(env, a.dir)
		if err != nil {
			return nil, err
		}
		if !found || !Stale(env, n.MtimeMax, days) {
			continue
		}
		f := nodeFinding(n)
		f.NativeArg = a.name
		f.Title = "Android emulator " + avdTitle(a)
		out = append(out, f)
	}
	return out, nil
}

// detectAVDSnapshots emits each AVD's snapshots directory that the scan kept.
// Small or empty snapshot folders (under the keep threshold) aren't worth a
// finding, so they're skipped rather than shown with an unknown size.
func detectAVDSnapshots(_ context.Context, env Env, _ Recipe) ([]Finding, error) {
	if env.Nodes == nil {
		return nil, nil
	}
	avds, err := ListAVDs(env)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, a := range avds {
		if !a.ok {
			continue
		}
		snap := filepath.Join(a.dir, "snapshots")
		if fi, err := os.Lstat(snap); err != nil || !fi.IsDir() {
			continue
		}
		n, found, err := ScanNode(env, snap)
		if err != nil {
			return nil, err
		}
		if found && n.Bytes > 0 {
			f := nodeFinding(n)
			f.Title = "Snapshots of emulator " + avdTitle(a)
			out = append(out, f)
		}
	}
	return out, nil
}

// ---- android.system_images_unused ----

// detectSystemImages emits <sdk>/system-images/<api>/<tag>/<abi> directories
// that no AVD's config.ini points at (image.sysdir.*).
func detectSystemImages(_ context.Context, env Env, _ Recipe) ([]Finding, error) {
	root, ok := AndroidSDKRoot(env)
	if !ok || env.Nodes == nil {
		return nil, nil
	}
	avds, err := ListAVDs(env)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, a := range avds {
		for k, v := range a.cfg {
			if !strings.HasPrefix(k, "image.sysdir.") || v == "" {
				continue
			}
			p := filepath.Clean(v)
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			used[strings.ToLower(p)] = true
		}
	}
	base := filepath.Join(root, "system-images")
	apis, err := subdirs(base)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, api := range apis {
		tags, err := subdirs(filepath.Join(base, api))
		if err != nil {
			return nil, err
		}
		for _, tag := range tags {
			abis, err := subdirs(filepath.Join(base, api, tag))
			if err != nil {
				return nil, err
			}
			for _, abi := range abis {
				p := filepath.Join(base, api, tag, abi)
				if used[strings.ToLower(p)] {
					continue
				}
				f, err := dirFinding(env, p, fmt.Sprintf("Android system image %s %s %s", api, tag, abi))
				if err != nil {
					return nil, err
				}
				f.Note = fmt.Sprintf("No emulator uses it. Package: system-images;%s;%s;%s", api, tag, abi)
				out = append(out, f)
			}
		}
	}
	return out, nil
}

// ---- android.build_tools_old / android.ndk_old ----

var sdkSubdirTitles = map[string]string{"build-tools": "Android build-tools", "ndk": "Android NDK"}

// DetectAndroidOldVersions emits every version directory of
// <sdk>/<params.subdir> except the newest.
func DetectAndroidOldVersions(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	sub := r.Params["subdir"]
	title, known := sdkSubdirTitles[sub]
	if !known {
		return nil, fmt.Errorf("recipe %s: detector android_old_versions needs params.subdir build-tools or ndk", r.ID)
	}
	root, ok := AndroidSDKRoot(env)
	if !ok || env.Nodes == nil {
		return nil, nil
	}
	names, err := subdirs(filepath.Join(root, sub))
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, name := range allButNewest(names, ParseVersion) {
		f, err := dirFinding(env, filepath.Join(root, sub, name), title+" "+name)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// ---- android_studio.old_versions_data ----

var studioParents = []string{"Library/Application Support/Google", "Library/Caches/Google", "Library/Logs/Google"}

var studioRE = regexp.MustCompile(`^AndroidStudio(\d+(?:\.\d+)*)$`)

func studioVersion(n model.Node) []int {
	m := studioRE.FindStringSubmatch(filepath.Base(n.Path))
	if m == nil {
		return nil
	}
	return ParseVersion(m[1])
}

// detectAndroidStudioOld emits AndroidStudio<ver> folders when a newer
// AndroidStudio<ver> exists in the same parent. Preview builds
// (AndroidStudioPreview…) never match.
func detectAndroidStudioOld(_ context.Context, env Env, _ Recipe) ([]Finding, error) {
	if env.Nodes == nil {
		return nil, nil
	}
	var out []Finding
	for _, p := range studioParents {
		kids, err := scanChildren(env, filepath.Join(env.Home, p))
		if err != nil {
			return nil, err
		}
		kind := strings.TrimSuffix(strings.TrimPrefix(p, "Library/"), "/Google")
		for _, n := range allButNewest(kids, studioVersion) {
			f := nodeFinding(n)
			f.Title = "Android Studio " + strings.TrimPrefix(filepath.Base(n.Path), "AndroidStudio") + " " + kind
			out = append(out, f)
		}
	}
	return out, nil
}
