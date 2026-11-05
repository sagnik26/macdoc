package tests

import (
	"bufio"
	"bytes"
	"context"
	"macdoc/internal/recipes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"macdoc/internal/model"
	"macdoc/internal/safety"
)

// androidHome builds a temporary home with real directories (the Android
// detectors list them) and clears the Android environment variables.
func androidHome(t *testing.T) string {
	t.Helper()
	for _, v := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT", "ANDROID_AVD_HOME", "ANDROID_EMULATOR_HOME"} {
		t.Setenv(v, "")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "Users", "dev")
	mkdir(t, home)
	return home
}

func mkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// addAVD registers an AVD the way avdmanager does: <name>.ini pointing at
// <name>.avd, whose config.ini names the system image.
func addAVD(t *testing.T, avdDir, name, dataDir, sysdir string) {
	t.Helper()
	writeFile(t, filepath.Join(avdDir, name+".ini"), "avd.ini.encoding=UTF-8\npath="+dataDir+"\npath.rel=avd/"+name+".avd\ntarget=android-34\n")
	writeFile(t, filepath.Join(dataDir, "config.ini"), "avd.ini.displayname="+strings.ReplaceAll(name, "_", " ")+"\nimage.sysdir.1="+sysdir+"\nhw.ramSize=2048\n")
}

// checkTargets asserts every quarantine finding passes the classifier's
// safety check with the recipe's allow patterns.
func checkTargets(t *testing.T, home, id string, fs []recipes.Finding) {
	t.Helper()
	pol, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := recipes.Load()
	r, _ := c.Get(id)
	for _, f := range fs {
		if err := pol.CheckTarget(f.Target, r.Allow); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestGradleCaches(t *testing.T) {
	p := me + "/.gradle/caches"
	if fs := mustDetect(t, "gradle.caches", recipes.Env{Home: me, Nodes: fakeNodes{{Path: p, Bytes: 7}}}); len(fs) != 1 || fs[0].Target != p {
		t.Errorf("caches = %+v", fs)
	}
	if fs := mustDetect(t, "gradle.caches", recipes.Env{Home: me, Nodes: fakeNodes{{Path: me + "/.gradle/caches-old", Bytes: 7}}}); fs != nil {
		t.Errorf("look-alike = %+v", fs)
	}
}

func TestGradleWrapperOld(t *testing.T) {
	home := androidHome(t)
	d := home + "/.gradle/wrapper/dists/"
	names := []string{"gradle-7.6-all", "gradle-8.5-bin", "gradle-8.10.2-all", "gradle-8.10.2-bin", "gradle-8.11-rc-1-bin", "notes"}
	nodes := fakeNodes{{Path: strings.TrimSuffix(d, "/"), Bytes: 1}}
	for _, n := range names {
		mkdir(t, d+n)
		nodes = append(nodes, model.Node{Path: d + n, Bytes: 100 << 20})
	}
	fs := mustDetect(t, "gradle.wrapper_dists_old", recipes.Env{Home: home, Nodes: nodes})
	if got := targets(fs); !slices.Equal(got, []string{d + "gradle-7.6-all", d + "gradle-8.5-bin"}) {
		t.Fatalf("targets = %v", got)
	}
	if fs[0].Title != "Gradle wrapper 7.6-all" && fs[1].Title != "Gradle wrapper 7.6-all" {
		t.Errorf("titles = %q, %q", fs[0].Title, fs[1].Title)
	}
	checkTargets(t, home, "gradle.wrapper_dists_old", fs)

	// Only the newest version: nothing to emit.
	if fs := mustDetect(t, "gradle.wrapper_dists_old", recipes.Env{Home: home, Nodes: fakeNodes{{Path: d + "gradle-8.10.2-bin", Bytes: 1}}}); fs != nil {
		t.Errorf("single = %+v", fs)
	}
	if fs := mustDetect(t, "gradle.wrapper_dists_old", recipes.Env{Home: home}); fs != nil {
		t.Error("no scan should give no findings")
	}
}

// avdFixture builds three registered AVDs plus a bad name, and scan nodes.
func avdFixture(t *testing.T) (home string, nodes fakeNodes) {
	home = androidHome(t)
	avdDir := home + "/.android/avd"
	addAVD(t, avdDir, "Pixel_7", avdDir+"/Pixel_7.avd", "system-images/android-34/google_apis/arm64-v8a/")
	addAVD(t, avdDir, "Fresh", avdDir+"/Fresh.avd", "system-images/android-35/default/arm64-v8a/")
	addAVD(t, avdDir, "Custom", home+"/emus/Custom.avd", "system-images/android-34/google_apis/arm64-v8a/")
	addAVD(t, avdDir, "bad name", avdDir+"/bad name.avd", "")
	mkdir(t, avdDir+"/Pixel_7.avd/snapshots", avdDir+"/Fresh.avd/snapshots", avdDir+"/Orphan.avd")
	writeFile(t, home+"/emus/Custom.avd/snapshots", "not a directory")
	mkdir(t, avdDir+"/Notes.ini") // a directory named *.ini is not a registry entry
	old, recent := mtime("2026-01-01"), mtime("2026-10-25")
	nodes = fakeNodes{
		{Path: avdDir, Bytes: 1},
		{Path: avdDir + "/Pixel_7.avd", Bytes: 8 << 30, MtimeMax: old},
		{Path: avdDir + "/Pixel_7.avd/snapshots", Bytes: 2 << 30, MtimeMax: old},
		{Path: avdDir + "/Fresh.avd", Bytes: 6 << 30, MtimeMax: recent},
		{Path: avdDir + "/bad name.avd", Bytes: 6 << 30, MtimeMax: old},
		{Path: home + "/emus/Custom.avd", Bytes: 3 << 30, MtimeMax: old},
	}
	return home, nodes
}

func TestAndroidAVDs(t *testing.T) {
	home, nodes := avdFixture(t)
	withTools(t, "avdmanager")
	fs := mustDetect(t, "android.avd", recipes.Env{Home: home, Nodes: nodes, Now: w1Now})
	// Custom's data directory is outside the AVD home, so it is skipped
	// since P3-W4 D5 (avdmanager deletes whatever path= names).
	if len(fs) != 1 {
		t.Fatalf("avds = %+v", fs)
	}
	if fs[0].NativeArg != "Pixel_7" || fs[0].Title != "Android emulator Pixel 7" ||
		fs[0].Bytes != (model.Bytes{Value: 8 << 30, Source: model.SourceMeasured}) {
		t.Errorf("avds = %+v", fs)
	}
	withTools(t)
	if fs := mustDetect(t, "android.avd", recipes.Env{Home: home, Nodes: nodes, Now: w1Now}); fs != nil {
		t.Errorf("avdmanager missing = %+v", fs)
	}
	withTools(t, "avdmanager")
	if fs := mustDetect(t, "android.avd", recipes.Env{Home: androidHome(t), Nodes: nodes}); fs != nil {
		t.Errorf("no AVD home = %+v", fs)
	}
	// stale_days unset: default 60.
	if fs, err := recipes.DetectAndroidAVDs(context.Background(), recipes.Env{Home: home, Nodes: nodes, Now: w1Now}, recipes.Recipe{}); err != nil || len(fs) != 1 {
		t.Errorf("default = %+v, %v", fs, err)
	}
}

func TestAVDSnapshots(t *testing.T) {
	home, nodes := avdFixture(t)
	fs := mustDetect(t, "android.avd_snapshots", recipes.Env{Home: home, Nodes: nodes})
	snap := home + "/.android/avd/Pixel_7.avd/snapshots"
	if len(fs) != 1 || fs[0].Target != snap || fs[0].Bytes.Value != 2<<30 || fs[0].Title != "Snapshots of emulator Pixel 7" {
		t.Fatalf("snapshots = %+v", fs)
	}
	checkTargets(t, home, "android.avd_snapshots", fs)
	if fs := mustDetect(t, "android.avd_snapshots", recipes.Env{Home: home}); fs != nil {
		t.Error("no scan should give no findings")
	}
}

func TestAVDHomeOverrides(t *testing.T) {
	home := androidHome(t)
	t.Setenv("ANDROID_EMULATOR_HOME", "~/emu")
	if got := recipes.AvdHome(recipes.Env{Home: home}); got != home+"/emu/avd" {
		t.Errorf("emulator home = %q", got)
	}
	t.Setenv("ANDROID_AVD_HOME", home+"/avds/")
	if got := recipes.AvdHome(recipes.Env{Home: home}); got != home+"/avds" {
		t.Errorf("avd home = %q", got)
	}
}

// sdkFixture builds an SDK with three system images, one used by an AVD.
func sdkFixture(t *testing.T, home, sdk string) fakeNodes {
	t.Helper()
	si := sdk + "/system-images/"
	mkdir(t, si+"android-34/google_apis/arm64-v8a", si+"android-35/google_apis_playstore/arm64-v8a", si+"android-33/default/arm64-v8a")
	writeFile(t, si+"android-33/README", "a file, not an image")
	avdDir := home + "/.android/avd"
	addAVD(t, avdDir, "Pixel_7", avdDir+"/Pixel_7.avd", "system-images/android-34/google_apis/arm64-v8a/")
	return fakeNodes{
		{Path: si + "android-34/google_apis/arm64-v8a", Bytes: 6 << 30},
		{Path: si + "android-35/google_apis_playstore/arm64-v8a", Bytes: 7 << 30, MtimeMax: mtime("2025-05-01")},
	}
}

func TestSystemImages(t *testing.T) {
	home := androidHome(t)
	sdk := home + "/Library/Android/sdk"
	nodes := sdkFixture(t, home, sdk)
	fs := mustDetect(t, "android.system_images_unused", recipes.Env{Home: home, Nodes: nodes})
	si := sdk + "/system-images/"
	if got := targets(fs); !slices.Equal(got, []string{si + "android-33/default/arm64-v8a", si + "android-35/google_apis_playstore/arm64-v8a"}) {
		t.Fatalf("targets = %v", got)
	}
	for _, f := range fs {
		switch {
		case strings.Contains(f.Target, "android-35"):
			if f.Bytes != (model.Bytes{Value: 7 << 30, Source: model.SourceMeasured}) || f.Title != "Android system image android-35 google_apis_playstore arm64-v8a" ||
				!strings.Contains(f.Note, "system-images;android-35;google_apis_playstore;arm64-v8a") {
				t.Errorf("image = %+v", f)
			}
		default:
			if f.Bytes.Source != model.SourceUnknown || f.Bytes.Reason == "" {
				t.Errorf("unscanned image = %+v", f.Bytes)
			}
		}
	}
	checkTargets(t, home, "android.system_images_unused", fs)

	// An absolute image.sysdir also counts as a reference.
	addAVD(t, home+"/.android/avd", "Abs", home+"/.android/avd/Abs.avd", si+"android-35/google_apis_playstore/arm64-v8a")
	if fs := mustDetect(t, "android.system_images_unused", recipes.Env{Home: home, Nodes: nodes}); len(fs) != 1 {
		t.Errorf("absolute sysdir = %v", targets(fs))
	}

	// An unreadable AVD registry is an error, never "nothing uses it".
	writeFile(t, home+"/.android/avd/Broken.ini", "path="+home+"/.android/avd/Broken.avd\n")
	writeFile(t, home+"/.android/avd/Broken.avd", "a file where config.ini's directory should be")
	if _, err := detect(t, "android.system_images_unused", recipes.Env{Home: home, Nodes: nodes}); err == nil {
		t.Error("unreadable config.ini accepted")
	}
	writeFile(t, home+"/.android/x", "")
	t.Setenv("ANDROID_AVD_HOME", home+"/.android/x")
	if _, err := detect(t, "android.system_images_unused", recipes.Env{Home: home, Nodes: nodes}); err == nil {
		t.Error("AVD home that is a file accepted")
	}
}

func TestSystemImagesSDKRoots(t *testing.T) {
	home := androidHome(t)
	// A non-default SDK root inside home still gives targets (the classifier
	// drops them if they don't match the allow patterns).
	alt := home + "/sdk"
	nodes := sdkFixture(t, home, alt)
	t.Setenv("ANDROID_SDK_ROOT", alt)
	if fs := mustDetect(t, "android.system_images_unused", recipes.Env{Home: home, Nodes: nodes}); len(fs) != 2 {
		t.Errorf("ANDROID_SDK_ROOT = %v", targets(fs))
	}
	// ANDROID_HOME wins over ANDROID_SDK_ROOT; outside home gives nothing.
	t.Setenv("ANDROID_HOME", "/opt/homebrew/share/android-commandlinetools")
	if fs := mustDetect(t, "android.system_images_unused", recipes.Env{Home: home, Nodes: nodes}); fs != nil {
		t.Errorf("outside home = %+v", fs)
	}
	t.Setenv("ANDROID_HOME", "relative/sdk")
	if _, ok := recipes.AndroidSDKRoot(recipes.Env{Home: home}); ok {
		t.Error("relative SDK root accepted")
	}
	// No SDK at all.
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	if fs := mustDetect(t, "android.system_images_unused", recipes.Env{Home: home, Nodes: nodes}); fs != nil {
		t.Errorf("no SDK = %+v", fs)
	}
	if fs := mustDetect(t, "android.system_images_unused", recipes.Env{Home: home}); fs != nil {
		t.Error("no scan should give no findings")
	}
}

// realSDK recreates the dev Mac's SDK layout (testdata/android_sdk_listing.txt)
// under home and returns scan nodes for every version directory.
func realSDK(t *testing.T, home string) fakeNodes {
	t.Helper()
	var nodes fakeNodes
	dir := ""
	sc := bufio.NewScanner(bytes.NewReader(readFixture(t, "android_sdk_listing.txt")))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
		case strings.HasSuffix(line, ":"):
			_, rel, _ := strings.Cut(strings.TrimSuffix(line, ":"), "/Library/Android/sdk/")
			dir = home + "/Library/Android/sdk/" + rel
		default:
			mkdir(t, dir+"/"+line)
			nodes = append(nodes, model.Node{Path: dir + "/" + line, Bytes: 150 << 20})
		}
	}
	return nodes
}

func TestAndroidOldVersions(t *testing.T) {
	home := androidHome(t)
	nodes := realSDK(t, home)
	bt := home + "/Library/Android/sdk/build-tools/"
	fs := mustDetect(t, "android.build_tools_old", recipes.Env{Home: home, Nodes: nodes})
	if got := targets(fs); !slices.Equal(got, []string{bt + "34.0.0", bt + "35.0.0"}) {
		t.Fatalf("build-tools = %v", got)
	}
	if fs[0].Title != "Android build-tools 34.0.0" || fs[0].Bytes.Source != model.SourceMeasured {
		t.Errorf("finding = %+v", fs[0])
	}
	checkTargets(t, home, "android.build_tools_old", fs)

	// Real fixture: a single NDK, which is the newest, so nothing.
	if fs := mustDetect(t, "android.ndk_old", recipes.Env{Home: home, Nodes: nodes}); fs != nil {
		t.Errorf("ndk = %+v", fs)
	}
	mkdir(t, home+"/Library/Android/sdk/ndk/26.3.11579264", home+"/Library/Android/sdk/ndk/27.0.12077973-beta1")
	fs = mustDetect(t, "android.ndk_old", recipes.Env{Home: home, Nodes: nodes})
	if len(fs) != 1 || !strings.HasSuffix(fs[0].Target, "/26.3.11579264") || fs[0].Bytes.Source != model.SourceUnknown {
		t.Errorf("ndk = %+v", fs)
	}
	checkTargets(t, home, "android.ndk_old", fs)

	if _, err := recipes.DetectAndroidOldVersions(context.Background(), recipes.Env{Home: home, Nodes: nodes}, recipes.Recipe{ID: "x.y", Params: map[string]string{"subdir": "../.."}}); err == nil {
		t.Error("bad subdir accepted")
	}
	if fs := mustDetect(t, "android.build_tools_old", recipes.Env{Home: home}); fs != nil {
		t.Error("no scan should give no findings")
	}
	if fs := mustDetect(t, "android.build_tools_old", recipes.Env{Home: androidHome(t), Nodes: nodes}); fs != nil {
		t.Errorf("no SDK = %+v", fs)
	}
	t.Setenv("ANDROID_HOME", home+"/Library/Android/sdk/ndk/26.3.11579264")
	writeFile(t, home+"/Library/Android/sdk/ndk/26.3.11579264/build-tools", "a file")
	if _, err := detect(t, "android.build_tools_old", recipes.Env{Home: home, Nodes: nodes}); err == nil {
		t.Error("unreadable build-tools accepted")
	}
}

func TestAndroidStudioOld(t *testing.T) {
	home := androidHome(t)
	as, ca, lg := home+"/Library/Application Support/Google", home+"/Library/Caches/Google", home+"/Library/Logs/Google"
	nodes := fakeNodes{
		{Path: as, Bytes: 1},
		{Path: as + "/AndroidStudio2023.3", Bytes: 900 << 20},
		{Path: as + "/AndroidStudio2024.1", Bytes: 900 << 20},
		{Path: as + "/AndroidStudioPreview2024.2", Bytes: 900 << 20},
		{Path: as + "/Chrome", Bytes: 900 << 20},
		{Path: ca + "/AndroidStudio2024.1", Bytes: 2 << 30},
		{Path: lg + "/AndroidStudio4.2", Bytes: 10 << 20},
		{Path: lg + "/AndroidStudio2024.1", Bytes: 10 << 20},
	}
	fs := mustDetect(t, "android_studio.old_versions_data", recipes.Env{Home: home, Nodes: nodes})
	if got := targets(fs); !slices.Equal(got, []string{as + "/AndroidStudio2023.3", lg + "/AndroidStudio4.2"}) {
		t.Fatalf("targets = %v", got)
	}
	for _, f := range fs {
		if f.Title != "Android Studio 2023.3 Application Support" && f.Title != "Android Studio 4.2 Logs" {
			t.Errorf("title = %q", f.Title)
		}
	}
	checkTargets(t, home, "android_studio.old_versions_data", fs)
	if fs := mustDetect(t, "android_studio.old_versions_data", recipes.Env{Home: home}); fs != nil {
		t.Error("no scan should give no findings")
	}
}

func TestAndroidScanErrors(t *testing.T) {
	home, _ := avdFixture(t)
	sdkFixture(t, home, home+"/Library/Android/sdk")
	realSDK(t, home)
	withTools(t, "avdmanager")
	env := recipes.Env{Home: home, Nodes: errNodes{}, Now: w1Now}
	for _, id := range []string{
		"gradle.wrapper_dists_old", "android.avd", "android.avd_snapshots", "android.system_images_unused",
		"android.build_tools_old", "android_studio.old_versions_data",
	} {
		if _, err := detect(t, id, env); err == nil {
			t.Errorf("%s: scan error swallowed", id)
		}
	}
}
