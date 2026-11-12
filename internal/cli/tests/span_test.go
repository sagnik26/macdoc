package tests

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// These tests cover how `doctor explain` shows the range of space a removal
// frees (ADR 0005, stage 3): the words, the JSON fields, and a whole run on a
// real scan of real APFS clones.

func ptr(v int64) *int64 { return &v }

// flat collapses line wrapping so a sentence can be matched whole.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func renderRange(t *testing.T, lower, upper *int64) string {
	t.Helper()
	var sb strings.Builder
	ex := cli.ExplainResult{
		Opportunity: model.Opportunity{ID: "O-eeeeee", Title: "x", Method: model.MethodQuarantine, Bytes: measured(1_200_000_000)},
		BytesLower:  lower, BytesUpper: upper,
	}
	if err := cli.RenderExplain(&sb, ex); err != nil {
		t.Fatal(err)
	}
	return flat(sb.String())
}

func TestExplainWordsForARange(t *testing.T) {
	note := flat(cli.SizeNote)
	cases := []struct {
		name         string
		lower, upper *int64
		want         []string
		not          []string
	}{
		{"a range", ptr(1_200_000_000), ptr(1_900_000_000),
			[]string{"Removing this frees at least 1.2 GB and at most 1.9 GB. The difference is space shared with copies of its files."}, []string{note}},
		{"no upper end", ptr(1_200_000_000), nil,
			[]string{"Removing this frees at least 1.2 GB. The most it could free could not be worked out."}, []string{"at most", note}},
		{"both ends equal", ptr(1_200_000_000), ptr(1_200_000_000), []string{note}, []string{"at most", "at least"}},
		{"not measured", nil, nil, []string{note}, []string{"at most", "at least"}},
		{"a known zero lower end", ptr(0), ptr(1_900_000_000),
			[]string{"at least 0 B and at most 1.9 GB"}, nil},
	}
	for _, c := range cases {
		out := renderRange(t, c.lower, c.upper)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q in:\n%s", c.name, w, out)
			}
		}
		for _, n := range c.not {
			if strings.Contains(out, n) {
				t.Errorf("%s: unexpected %q in:\n%s", c.name, n, out)
			}
		}
		for _, bad := range []string{"danger", "warning", "unsafe", "junk"} {
			if strings.Contains(strings.ToLower(out), bad) {
				t.Errorf("%s: scare word %q", c.name, bad)
			}
		}
	}
}

func TestExplainJSONFieldsAreOptional(t *testing.T) {
	enc := func(ex cli.ExplainResult) map[string]json.RawMessage {
		b, err := json.Marshal(ex)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := enc(cli.ExplainResult{})
	if _, ok := m["bytes_lower"]; ok {
		t.Error("bytes_lower present without a measurement")
	}
	if _, ok := m["bytes_upper"]; ok {
		t.Error("bytes_upper present without a measurement")
	}
	m = enc(cli.ExplainResult{BytesLower: ptr(0), BytesUpper: ptr(7)})
	if string(m["bytes_lower"]) != "0" || string(m["bytes_upper"]) != "7" {
		t.Errorf("fields = %s, %s; want a measured 0 kept and 7", m["bytes_lower"], m["bytes_upper"])
	}
	m = enc(cli.ExplainResult{BytesLower: ptr(5)})
	if _, ok := m["bytes_upper"]; ok {
		t.Error("an unknown upper end was written")
	}
}

// realClones scans a fake home whose DerivedData folder holds a clone group
// wholly inside it and installs the home for the CLI. It returns the ID of
// the suggestion for the folder.
func realClones(t *testing.T, shared bool) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	dd := filepath.Join(home, "Library/Developer/Xcode/DerivedData/App-1")
	fstree.Build(t, home, fstree.File("Library/Developer/Xcode/DerivedData/App-1/own.bin", 2<<20))
	if shared {
		fstree.Build(t, dd, fstree.File("g.bin", 4<<20))
		if out, err := exec.Command("cp", "-c", filepath.Join(dd, "g.bin"), filepath.Join(dd, "g1.bin")).CombinedOutput(); err != nil {
			t.Skipf("no clone support here: cp -c: %v: %s", err, out)
		}
	}
	old := time.Now().Add(-100 * 24 * time.Hour)
	entries, err := os.ReadDir(dd)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(dd, e.Name()), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(dd, old, old); err != nil {
		t.Fatal(err)
	}
	if err := unix.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Scan(context.Background(), engine.ScanOptions{Home: home, Root: root}); err != nil {
		t.Fatal(err)
	}

	full, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := full.Get("xcode.derived_data")
	cat := &recipes.Catalog{Recipes: []recipes.Recipe{r}}
	oldHome, oldCat, oldRunner, oldTool := cli.UserHome, cli.LoadCatalog, cli.Runner, recipes.ToolInstalled
	cli.UserHome = func() (string, error) { return home, nil }
	cli.LoadCatalog = func() (*recipes.Catalog, error) { return cat, nil }
	cli.Runner = &platform.Fake{}
	recipes.ToolInstalled = func(string) bool { return true }
	t.Cleanup(func() {
		cli.UserHome, cli.LoadCatalog, cli.Runner, recipes.ToolInstalled = oldHome, oldCat, oldRunner, oldTool
	})
	res, err := engine.Opportunities(context.Background(), engine.OpportunityOptions{Home: home, Catalog: cat, MinBytes: -1})
	if err != nil || len(res.Opportunities) != 1 {
		t.Fatalf("opportunities: %v, %v", res.Opportunities, err)
	}
	return res.Opportunities[0].ID
}
