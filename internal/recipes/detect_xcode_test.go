package recipes

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/platform"
)

// w1Now is the fixed clock for staleness tests.
var w1Now = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

// withTools makes ToolInstalled report exactly the given tools.
func withTools(t *testing.T, names ...string) {
	t.Helper()
	old := ToolInstalled
	ToolInstalled = func(n string) bool { return slices.Contains(names, n) }
	t.Cleanup(func() { ToolInstalled = old })
}

// detect runs the embedded catalog's recipe id with its own detector and params.
func detect(t *testing.T, id string, env Env) ([]Finding, error) {
	t.Helper()
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := c.Get(id)
	if !ok {
		t.Fatalf("recipe %s not in catalog", id)
	}
	d, ok := Lookup(r.Detector)
	if !ok {
		t.Fatalf("detector %s not registered", r.Detector)
	}
	return d(context.Background(), env, r)
}

// mustDetect is detect that fails on an error.
func mustDetect(t *testing.T, id string, env Env) []Finding {
	t.Helper()
	fs, err := detect(t, id, env)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return fs
}

func targets(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Target)
	}
	slices.Sort(out)
	return out
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mtime(s string) int64 {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t.Unix()
}

const (
	me      = "/Users/me"
	devsDir = me + "/Library/Developer/CoreSimulator/Devices/"
	keyDevs = "xcrun simctl list -j devices"
	keyRTs  = "xcrun simctl runtime list -j"
	keyFind = "xcrun --find simctl"
)

// simctlMissing is the real CLT-only response (testdata/simctl_unavailable.txt).
func simctlMissing(t *testing.T) platform.Result {
	return platform.Result{Stderr: readFixture(t, "simctl_unavailable.txt"), ExitCode: 72}
}

func TestW1CatalogLoads(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"xcode.derived_data", "xcode.device_support", "xcode.previews_cache", "xcode.caches",
		"xcode.archives", "xcode.simulators_unavailable", "xcode.sim_runtimes",
		"xcode.sim_runtimes_orphaned", "xcode.sim_devices_idle", "cocoapods.cache",
		"gradle.caches", "gradle.wrapper_dists_old", "android.avd", "android.avd_snapshots",
		"android.system_images_unused", "android.build_tools_old", "android.ndk_old",
		"android_studio.old_versions_data",
	}
	for _, id := range ids {
		r, ok := c.Get(id)
		if !ok {
			t.Errorf("%s missing", id)
			continue
		}
		if r.Restore.Note == "" {
			t.Errorf("%s: no restore note", id)
		}
		if n := strings.Count(strings.TrimSpace(r.Explain), ". ") + 1; n < 2 || n > 5 {
			t.Errorf("%s: explain has %d sentences", id, n)
		}
	}
}

func TestDerivedData(t *testing.T) {
	dd := me + "/Library/Developer/Xcode/DerivedData"
	env := Env{Home: me, Nodes: fakeNodes{
		{Path: dd, Bytes: 300}, {Path: dd + "/App-abc", Bytes: 200, MtimeMax: mtime("2026-01-01")},
		{Path: dd + "/Other-def", Bytes: 100},
		{Path: dd + ".bak", Bytes: 900}, {Path: dd + ".bak/Old-xyz", Bytes: 900},
	}}
	fs := mustDetect(t, "xcode.derived_data", env)
	if got := targets(fs); !slices.Equal(got, []string{dd + "/App-abc", dd + "/Other-def"}) {
		t.Fatalf("targets = %v", got)
	}
}

func TestDeviceSupport(t *testing.T) {
	x := me + "/Library/Developer/Xcode/"
	ios, watch := x+"iOS DeviceSupport", x+"watchOS DeviceSupport"
	env := Env{Home: me, Nodes: fakeNodes{
		{Path: ios, Bytes: 10},
		{Path: ios + "/16.4 (20E247)", Bytes: 3 << 30, MtimeMax: mtime("2024-01-01")},
		{Path: ios + "/17.2 (21C62)", Bytes: 4 << 30},
		{Path: ios + "/iPhone15,2 17.2.1 (21C66)", Bytes: 4 << 30},
		{Path: ios + "/iPhone14,5 17.2.1 (21C66)", Bytes: 4 << 30},
		{Path: ios + "/Latest", Bytes: 1 << 30},
		{Path: watch + "/10.2 (21S364)", Bytes: 1 << 30},
	}}
	fs := mustDetect(t, "xcode.device_support", env)
	if got := targets(fs); !slices.Equal(got, []string{ios + "/16.4 (20E247)", ios + "/17.2 (21C62)"}) {
		t.Fatalf("targets = %v", got)
	}
	for _, f := range fs {
		if f.Bytes.Source != model.SourceMeasured || f.Bytes.Value != 3<<30 && f.Bytes.Value != 4<<30 {
			t.Errorf("bytes = %+v", f.Bytes)
		}
		if !strings.HasPrefix(f.Title, "iOS device support ") {
			t.Errorf("title = %q", f.Title)
		}
	}
	if fs, _ := detect(t, "xcode.device_support", Env{Home: me}); fs != nil {
		t.Error("no scan should give no findings")
	}
}

func TestXcodePathRecipes(t *testing.T) {
	for id, p := range map[string]string{
		"xcode.previews_cache": me + "/Library/Developer/Xcode/UserData/Previews",
		"xcode.caches":         me + "/Library/Caches/com.apple.dt.Xcode",
	} {
		fs := mustDetect(t, id, Env{Home: me, Nodes: fakeNodes{{Path: p, Bytes: 500}}})
		if len(fs) != 1 || fs[0].Target != p || fs[0].Bytes.Value != 500 {
			t.Errorf("%s = %+v", id, fs)
		}
		if fs := mustDetect(t, id, Env{Home: me, Nodes: fakeNodes{{Path: p + ".bak", Bytes: 500}}}); fs != nil {
			t.Errorf("%s matched a look-alike: %+v", id, fs)
		}
	}
}

func TestArchivesAdviseOnly(t *testing.T) {
	a := me + "/Library/Developer/Xcode/Archives"
	fs := mustDetect(t, "xcode.archives", Env{Home: me, Nodes: fakeNodes{{Path: a, Bytes: 9}, {Path: a + "/2025-01-01", Bytes: 9}}})
	if len(fs) != 1 || fs[0].Target != a+"/2025-01-01" {
		t.Fatalf("archives = %+v", fs)
	}
	c, _ := Load()
	r, _ := c.Get("xcode.archives")
	if r.Method != model.MethodAdvise || r.Restore.Kind != model.RestoreIrreplaceable {
		t.Errorf("archives recipe = %+v", r)
	}
}

func TestSimctlUnavailable(t *testing.T) {
	withTools(t, "xcrun")
	devsJSON := readFixture(t, "w1_simctl_devices.json")
	runner := func(res platform.Result) *platform.FakeRunner {
		return &platform.FakeRunner{Responses: map[string]platform.Result{keyDevs: res}}
	}
	ok := platform.Result{Stdout: devsJSON}
	d1, d2 := devsDir+"0A1B2C3D-0000-4000-8000-000000000001", devsDir+"0A1B2C3D-0000-4000-8000-000000000002"

	// One measured by the scan, one sized by simctl's dataPathSize.
	fr := runner(ok)
	fs := mustDetect(t, "xcode.simulators_unavailable", Env{Home: me, Runner: fr, Nodes: fakeNodes{{Path: d1, Bytes: 5000}}})
	if len(fs) != 1 || fs[0].Bytes.Value != 5000+1073741824 || fs[0].Bytes.Source != model.SourceTool ||
		fs[0].Title != "2 unavailable simulator(s)" || fs[0].NativeArg != "" ||
		!strings.Contains(fs[0].Note, "iPhone 14 (iOS 16.4)") {
		t.Fatalf("unavailable = %+v", fs)
	}
	if len(fr.Calls) != 1 || platform.Key(fr.Calls[0]) != keyDevs {
		t.Errorf("calls = %+v", fr.Calls)
	}

	// Both measured.
	fs = mustDetect(t, "xcode.simulators_unavailable", Env{Home: me, Runner: runner(ok), Nodes: fakeNodes{{Path: d1, Bytes: 1}, {Path: d2, Bytes: 2}}})
	if fs[0].Bytes != (model.Bytes{Value: 3, Source: model.SourceMeasured}) {
		t.Errorf("measured = %+v", fs[0].Bytes)
	}

	// No sizes at all: unknown, never 0; partly unknown: a lower bound.
	noSize := []byte(strings.ReplaceAll(string(devsJSON), `"dataPathSize"`, `"ignored"`))
	fs = mustDetect(t, "xcode.simulators_unavailable", Env{Home: me, Runner: runner(platform.Result{Stdout: noSize})})
	if fs[0].Bytes.Source != model.SourceUnknown || fs[0].Bytes.Reason == "" {
		t.Errorf("unknown = %+v", fs[0].Bytes)
	}
	fs = mustDetect(t, "xcode.simulators_unavailable", Env{Home: me, Runner: runner(platform.Result{Stdout: noSize}), Nodes: fakeNodes{{Path: d1, Bytes: 7}}})
	if fs[0].Bytes.Source != model.SourceEstimated || fs[0].Bytes.Value != 7 || !strings.Contains(fs[0].Bytes.Reason, "1 of 2") {
		t.Errorf("estimated = %+v", fs[0].Bytes)
	}

	// Look-alike: every device available.
	allOK := []byte(strings.ReplaceAll(string(devsJSON), `"isAvailable" : false`, `"isAvailable" : true`))
	if fs := mustDetect(t, "xcode.simulators_unavailable", Env{Home: me, Runner: runner(platform.Result{Stdout: allOK})}); fs != nil {
		t.Errorf("all available = %+v", fs)
	}
}

func TestSimctlToolMissingAndErrors(t *testing.T) {
	for _, id := range []string{"xcode.simulators_unavailable", "xcode.sim_devices_idle", "xcode.sim_runtimes"} {
		key := keyDevs
		if id == "xcode.sim_runtimes" {
			key = keyRTs
		}
		nodes := fakeNodes{}
		withTools(t, "xcrun")
		missing := map[string]*platform.FakeRunner{
			"CLT only":  {Responses: map[string]platform.Result{key: simctlMissing(t)}},
			"not found": {Errors: map[string]error{key: errors.New(`exec: "xcrun": executable file not found in $PATH`)}},
		}
		for name, fr := range missing {
			if fs, err := detect(t, id, Env{Home: me, Runner: fr, Nodes: nodes}); fs != nil || err != nil {
				t.Errorf("%s %s: %+v, %v", id, name, fs, err)
			}
		}
		failing := map[string]*platform.FakeRunner{
			"timeout":  {Errors: map[string]error{key: context.DeadlineExceeded}},
			"exit":     {Responses: map[string]platform.Result{key: {ExitCode: 1, Stderr: []byte("CoreSimulatorService connection invalid")}}},
			"bad json": {Responses: map[string]platform.Result{key: {Stdout: []byte("{")}}},
		}
		for name, fr := range failing {
			if _, err := detect(t, id, Env{Home: me, Runner: fr, Nodes: nodes}); err == nil {
				t.Errorf("%s %s: no error", id, name)
			}
		}
		withTools(t)
		fr := &platform.FakeRunner{}
		if fs, err := detect(t, id, Env{Home: me, Runner: fr, Nodes: nodes}); fs != nil || err != nil || len(fr.Calls) != 0 {
			t.Errorf("%s without xcrun: %+v, %v, calls %v", id, fs, err, fr.Calls)
		}
	}
}

func TestSimctlIdle(t *testing.T) {
	withTools(t, "xcrun")
	fr := &platform.FakeRunner{Responses: map[string]platform.Result{keyDevs: {Stdout: readFixture(t, "w1_simctl_devices.json")}}}
	old, recent := mtime("2024-01-10"), mtime("2026-10-20")
	d3 := devsDir + "1A1B2C3D-0000-4000-8000-000000000003"
	env := Env{Home: me, Runner: fr, Now: w1Now, Nodes: fakeNodes{
		{Path: devsDir + "0A1B2C3D-0000-4000-8000-000000000001", Bytes: 9, MtimeMax: old},    // unavailable
		{Path: d3, Bytes: 3 << 30, MtimeMax: old},                                            // idle
		{Path: devsDir + "1A1B2C3D-0000-4000-8000-000000000004", Bytes: 9, MtimeMax: recent}, // in use
		{Path: devsDir + "1A1B2C3D-0000-4000-8000-000000000005", Bytes: 9, MtimeMax: old},    // booted
		{Path: devsDir + "not a udid; rm", Bytes: 9, MtimeMax: old},                          // bad identifier
	}}
	fs := mustDetect(t, "xcode.sim_devices_idle", env)
	if len(fs) != 1 {
		t.Fatalf("idle = %+v", fs)
	}
	f := fs[0]
	if f.Target != d3 || f.NativeArg != "1A1B2C3D-0000-4000-8000-000000000003" || f.Title != "Simulator iPhone 15 (iOS 17.2)" ||
		f.Bytes != (model.Bytes{Value: 3 << 30, Source: model.SourceMeasured}) || f.LastUsed.Unix() != old {
		t.Errorf("idle = %+v", f)
	}
	if fs := mustDetect(t, "xcode.sim_devices_idle", Env{Home: me, Runner: fr}); fs != nil {
		t.Error("no scan should give no findings")
	}
	r := Recipe{ID: "x.y"} // stale_days unset: default 90
	fs, err := detectSimctlIdle(context.Background(), env, r)
	if err != nil || len(fs) != 1 {
		t.Errorf("default stale days: %+v, %v", fs, err)
	}
}

func TestSimctlRuntimes(t *testing.T) {
	withTools(t, "xcrun")
	fr := &platform.FakeRunner{Responses: map[string]platform.Result{keyRTs: {Stdout: readFixture(t, "w1_simctl_runtime_list.json")}}}
	fs := mustDetect(t, "xcode.sim_runtimes", Env{Home: me, Runner: fr})
	want := []struct {
		arg, title string
		bytes      model.Bytes
		newest     bool
	}{
		{"5D5B4E2A-1C7F-4C0B-9E33-6A0D3B1F7A01", "iOS 17.2 simulator runtime", model.Bytes{Value: 7516192768, Source: model.SourceTool}, false},
		{"8E2C1F44-7B6A-4D9E-A1C2-3F4B5C6D7E02", "iOS 18.1 simulator runtime", model.Bytes{Value: 8589934592, Source: model.SourceTool}, true},
		{"A0B1C2D3-E4F5-4607-8899-AABBCCDDEE03", "watchOS 10.2 simulator runtime", model.Bytes{Source: model.SourceUnknown, Reason: "simctl did not report a size"}, true},
	}
	if len(fs) != len(want) {
		t.Fatalf("runtimes = %+v", fs)
	}
	for i, w := range want {
		f := fs[i]
		if f.NativeArg != w.arg || f.Title != w.title || f.Bytes != w.bytes || (f.Note != "") != w.newest {
			t.Errorf("runtime %d = %+v, want %+v", i, f, w)
		}
		if !strings.HasPrefix(f.Target, "/Library/Developer/CoreSimulator/Images/") {
			t.Errorf("target = %q", f.Target)
		}
	}
	if !fs[0].LastUsed.Equal(time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)) || !fs[2].LastUsed.IsZero() {
		t.Errorf("last used = %v, %v", fs[0].LastUsed, fs[2].LastUsed)
	}
}

func TestOrphanedSimRuntimes(t *testing.T) {
	assets := "/System/Library/AssetsV2/com_apple_MobileAsset_iOSSimulatorRuntime"
	dyld := "/Library/Developer/CoreSimulator/Caches/dyld"
	nodes := fakeNodes{
		{Path: assets, Bytes: 33_700_000_000}, {Path: assets + "/x.asset", Bytes: 33_700_000_000},
		{Path: dyld, Bytes: 15_200_000_000},
		{Path: "/Library/Developer/CoreSimulator/Images", Bytes: 0},
	}
	missing := &platform.FakeRunner{Responses: map[string]platform.Result{keyFind: simctlMissing(t)}}

	withTools(t, "xcrun")
	fs := mustDetect(t, "xcode.sim_runtimes_orphaned", Env{Home: me, Runner: missing, Nodes: nodes})
	if got := targets(fs); !slices.Equal(got, []string{dyld, assets}) {
		t.Fatalf("orphaned = %+v", fs)
	}
	for _, f := range fs {
		if f.Bytes.Source != model.SourceMeasured || f.Title == "" || f.NativeArg != "" {
			t.Errorf("finding = %+v", f)
		}
	}

	// No xcrun at all: also unavailable.
	withTools(t)
	if fs := mustDetect(t, "xcode.sim_runtimes_orphaned", Env{Home: me, Runner: &platform.FakeRunner{}, Nodes: nodes}); len(fs) != 2 {
		t.Errorf("no xcrun = %+v", fs)
	}

	// simctl works: xcode.sim_runtimes covers it.
	withTools(t, "xcrun")
	works := &platform.FakeRunner{Responses: map[string]platform.Result{keyFind: {Stdout: []byte("/Applications/Xcode.app/Contents/Developer/usr/bin/simctl\n")}}}
	if fs := mustDetect(t, "xcode.sim_runtimes_orphaned", Env{Home: me, Runner: works, Nodes: nodes}); fs != nil {
		t.Errorf("simctl available = %+v", fs)
	}
	for _, env := range []Env{{Home: me, Runner: works}, {Home: me, Nodes: nodes}} {
		if fs := mustDetect(t, "xcode.sim_runtimes_orphaned", env); fs != nil {
			t.Errorf("no scan / no runner = %+v", fs)
		}
	}
	for name, fr := range map[string]*platform.FakeRunner{
		"timeout": {Errors: map[string]error{keyFind: context.DeadlineExceeded}},
		"exit":    {Responses: map[string]platform.Result{keyFind: {ExitCode: 1, Stderr: []byte("boom")}}},
	} {
		if _, err := detect(t, "xcode.sim_runtimes_orphaned", Env{Home: me, Runner: fr, Nodes: nodes}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestCocoaPodsCache(t *testing.T) {
	p := me + "/Library/Caches/CocoaPods"
	env := Env{Home: me, Nodes: fakeNodes{{Path: p, Bytes: 42}}}
	withTools(t, "pod")
	if fs := mustDetect(t, "cocoapods.cache", env); len(fs) != 1 || fs[0].Target != p || fs[0].Bytes.Value != 42 {
		t.Errorf("pods = %+v", fs)
	}
	if fs := mustDetect(t, "cocoapods.cache", Env{Home: me, Nodes: fakeNodes{{Path: p + "-old", Bytes: 42}}}); fs != nil {
		t.Errorf("look-alike = %+v", fs)
	}
	withTools(t)
	if fs := mustDetect(t, "cocoapods.cache", env); fs != nil {
		t.Errorf("pod not installed = %+v", fs)
	}
}

func TestNativeArgValidation(t *testing.T) {
	for _, s := range []string{"Pixel_7", "1A1B2C3D-0000-4000-8000-000000000003", "com.apple.CoreSimulator.SimRuntime.iOS-17-2"} {
		if !validNativeArg(s) {
			t.Errorf("rejected %q", s)
		}
	}
	for _, s := range []string{"", "-rf", "a b", "a/b", "a;b", "$(x)", "a`b", "a\nb", "a|b", strings.Repeat("a", 256)} {
		if validNativeArg(s) {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestXcodeHelpers(t *testing.T) {
	if runtimeName("com.apple.CoreSimulator.SimRuntime.iOS-17-2") != "iOS 17.2" || runtimeName("Weird") != "Weird" {
		t.Error("runtimeName")
	}
	if parseVersion("1.x") != nil || parseVersion("99999999999999999999") != nil || !slices.Equal(parseVersion("8.10.2"), []int{8, 10, 2}) {
		t.Error("parseVersion")
	}
	if (Env{}).now().IsZero() || idleFor(Env{Now: w1Now}, 0, 1) {
		t.Error("now/idleFor")
	}
	if toolMissing(platform.Result{}, nil) {
		t.Error("success is not a missing tool")
	}
	// A failing scan store is an error for every scan-based detector.
	bad := Env{Home: me, Nodes: errNodes{}, Runner: &platform.FakeRunner{}}
	if _, _, err := scanNode(bad, "/x"); err == nil {
		t.Error("scanNode error swallowed")
	}
	if _, err := detectDeviceSupport(context.Background(), bad, Recipe{}); err == nil {
		t.Error("device support error swallowed")
	}
}

type errNodes struct{}

func (errNodes) Nodes(string, int) ([]model.Node, error) { return nil, errors.New("store closed") }
