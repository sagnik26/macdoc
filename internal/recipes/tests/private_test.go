package tests

import (
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/projects"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// These tests cover ADR 0005 stage 2: a suggestion sized from a scan node
// shows what removing it is certain to free (the node's private size), and
// falls back to the allocated bytes only when the private size is unknown.

func ptr(n int64) *int64 { return &n }

func TestNodeSuggestionUsesPrivateSize(t *testing.T) {
	dd := me + "/Library/Developer/Xcode/DerivedData"
	nodes := fakeNodes{
		{Path: dd, Bytes: 900},
		{Path: dd + "/Shared-a", Bytes: 300, Private: ptr(100)}, // clones of files elsewhere
		{Path: dd + "/Plain-b", Bytes: 200},                     // private unknown: fallback reader
		{Path: dd + "/Allshared-c", Bytes: 400, Private: ptr(0)},
	}
	fs := mustDetect(t, "xcode.derived_data", recipes.Env{Home: me, Nodes: nodes})
	got := map[string]model.Bytes{}
	for _, f := range fs {
		got[f.Target] = f.Bytes
	}
	cases := map[string]model.Bytes{
		dd + "/Shared-a":    {Value: 100, Source: model.SourceMeasured},
		dd + "/Plain-b":     {Value: 200, Source: model.SourceMeasured},
		dd + "/Allshared-c": {Value: 0, Source: model.SourceMeasured},
	}
	if len(got) != len(cases) {
		t.Fatalf("findings = %+v", fs)
	}
	for target, want := range cases {
		if got[target] != want {
			t.Errorf("%s: size %+v, want %+v", target, got[target], want)
		}
	}
}

// A known 0 is a number, not a gap: it stays measured, where an unknown
// size is marked unknown and carries no value.
func TestKnownZeroIsNotUnknown(t *testing.T) {
	dd := me + "/Library/Developer/Xcode/DerivedData"
	nodes := fakeNodes{{Path: dd + "/Allshared-c", Bytes: 400, Private: ptr(0)}}
	fs := mustDetect(t, "xcode.derived_data", recipes.Env{Home: me, Nodes: nodes})
	if len(fs) != 1 || fs[0].Bytes.Source != model.SourceMeasured || fs[0].Bytes.Value != 0 {
		t.Fatalf("all-shared folder = %+v; want a measured 0", fs)
	}
}

// min_bytes still reads the allocated size, so which folders a recipe looks
// at does not depend on how much of them is shared.
func TestThresholdUsesAllocatedSize(t *testing.T) {
	dd := me + "/Library/Developer/Xcode/DerivedData"
	r := recipes.Recipe{ID: "x.y", Params: map[string]string{"dir": "~/Library/Developer/Xcode/DerivedData", "min_bytes": "250"}}
	nodes := fakeNodes{
		{Path: dd + "/Big-shared", Bytes: 300, Private: ptr(10)},
		{Path: dd + "/Small", Bytes: 200, Private: ptr(200)},
	}
	fs, err := recipes.DetectChildren(t.Context(), recipes.Env{Home: me, Nodes: nodes}, r)
	if err != nil || len(fs) != 1 || fs[0].Target != dd+"/Big-shared" || fs[0].Bytes.Value != 10 {
		t.Fatalf("got %+v, %v; want only Big-shared, sized 10", fs, err)
	}
}

func TestPathSuggestionUsesPrivateSize(t *testing.T) {
	p := me + "/.gradle/caches"
	env := recipes.Env{Home: me, Nodes: fakeNodes{{Path: p, Bytes: 1000, Private: ptr(640)}}}
	fs, err := recipes.DetectPath(t.Context(), env, recipes.Recipe{ID: "x.y", Params: map[string]string{"path": "~/.gradle/caches"}})
	if err != nil || len(fs) != 1 || fs[0].Bytes != (model.Bytes{Value: 640, Source: model.SourceMeasured}) {
		t.Fatalf("got %+v, %v", fs, err)
	}
	// A recipe that declines to claim a size stays unknown, whatever the node knows.
	fs, err = recipes.DetectPath(t.Context(), env, recipes.Recipe{ID: "x.y", Params: map[string]string{"path": "~/.gradle/caches", "size": "unknown"}})
	if err != nil || len(fs) != 1 || fs[0].Bytes.Source != model.SourceUnknown {
		t.Fatalf("size=unknown: got %+v, %v", fs, err)
	}
}

// Simulators: a device the scan measured counts by what deleting it frees;
// one the scan lacks keeps simctl's own number, still labelled as the tool's.
func TestSimulatorsUsePrivateSizeAndKeepToolSize(t *testing.T) {
	withTools(t, "xcrun")
	devs := readFixture(t, "w1_simctl_devices.json")
	runner := &platform.FakeRunner{Responses: map[string]platform.Result{keyDevs: {Stdout: devs}}}
	d1 := devsDir + "0A1B2C3D-0000-4000-8000-000000000001"
	env := recipes.Env{Home: me, Runner: runner, Nodes: fakeNodes{{Path: d1, Bytes: 5000, Private: ptr(1200)}}}
	fs := mustDetect(t, "xcode.simulators_unavailable", env)
	if len(fs) != 1 || fs[0].Bytes.Value != 1200+1073741824 || fs[0].Bytes.Source != model.SourceTool {
		t.Fatalf("got %+v; want the node's private 1200 plus simctl's 1073741824, source tool", fs)
	}
}

func TestProjectArtifactUsesPrivateSize(t *testing.T) {
	proj := p4Project()
	proj.Artifacts = []projects.Artifact{
		{Rel: "ios/Pods", Path: proj.Root + "/ios/Pods", Bytes: 300_000_000, Private: ptr(120_000_000)},
		{Rel: ".next", Path: proj.Root + "/.next", Bytes: 100_000_000},
	}
	fs, err := w2Run(t, "rn.ios_pods_stale", p4Env(proj))
	if err != nil || len(fs) != 1 || fs[0].Bytes != (model.Bytes{Value: 120_000_000, Source: model.SourceMeasured}) {
		t.Fatalf("pods = %+v, %v", fs, err)
	}
	fs, err = w2Run(t, "node.next_cache_stale", p4Env(proj))
	if err != nil || len(fs) != 1 || fs[0].Bytes.Value != 100_000_000 {
		t.Fatalf("next (private unknown) = %+v, %v; want the allocated size", fs, err)
	}
}
