package tests

import (
	"context"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
)

func TestDetectPathsUnit(t *testing.T) {
	home := "/Users/me"
	nodes := fakeNodes{
		{Path: home + "/a/one", Bytes: 10, MtimeMax: 1_700_000_000},
		{Path: home + "/a/two words", Bytes: 20},
		{Path: home + "/a/empty", Bytes: 0},
		{Path: home + "/a/other", Bytes: 99},
		{Path: home + "/a/one/inner", Bytes: 5},
	}
	env := recipes.Env{Home: home, Nodes: nodes}
	r := recipes.Recipe{ID: "x.y", Params: map[string]string{
		"paths": "~/a/one, ~/a/two words,~/a/missing,~/a/empty",
	}}
	fs, err := recipes.DetectPaths(context.Background(), env, r)
	w2WantTargets(t, fs, err, home+"/a/one", home+"/a/two words")
	for _, f := range fs {
		if f.Bytes.Source != model.SourceMeasured || f.Bytes.Value == 0 {
			t.Errorf("finding %+v not measured", f)
		}
	}
	// size=unknown applies to every finding.
	r.Params["size"] = "unknown"
	fs, _ = recipes.DetectPaths(context.Background(), env, r)
	for _, f := range fs {
		if f.Bytes.Source != model.SourceUnknown {
			t.Errorf("finding %+v should be unknown", f)
		}
	}
	// Nothing kept, no nodes, and a missing param.
	fs, err = recipes.DetectPaths(context.Background(), recipes.Env{Home: home, Nodes: fakeNodes{}}, r)
	w2WantNone(t, fs, err)
	fs, err = recipes.DetectPaths(context.Background(), recipes.Env{Home: home}, r)
	w2WantNone(t, fs, err)
	if _, err := recipes.DetectPaths(context.Background(), env, recipes.Recipe{ID: "x.y"}); err == nil {
		t.Error("missing params.paths should error")
	}
	if _, ok := recipes.Lookup("paths"); !ok {
		t.Error("paths detector not registered")
	}
}
