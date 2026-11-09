package tests

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// These tests cover the range a suggestion's explanation can show (ADR 0005,
// stage 3). They use real APFS clones made by cp -c and a real scan, and skip
// where the filesystem cannot clone.

// folder builds a fake home with one old DerivedData project folder. build
// fills it; it receives the folder and the home. It returns the home, the
// scan root and the folder.
func folder(t *testing.T, build func(dd, home string)) (home, root, dd string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(root, "home")
	dd = filepath.Join(home, "Library/Developer/Xcode/DerivedData/App-1")
	fstree.Build(t, home, fstree.File("Library/Developer/Xcode/DerivedData/App-1/own.bin", 2<<20), fstree.File("ref/shared.bin", 4<<20))
	build(dd, home)
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
	return home, root, dd
}

// cp clones src to dst or skips the test where there is no clonefile(2).
func cp(t *testing.T, src, dst string) {
	t.Helper()
	if out, err := exec.Command("cp", "-c", src, dst).CombinedOutput(); err != nil {
		t.Skipf("no clone support here: cp -c: %v: %s", err, out)
	}
}

// suggestion scans root and returns the DerivedData suggestion for dd.
func suggestion(t *testing.T, home, root, dd string) model.Opportunity {
	t.Helper()
	scanned(t, home, root, dd)
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := cat.Get("xcode.derived_data")
	stubTools(t)
	res, err := engine.Opportunities(context.Background(), engine.OpportunityOptions{Home: home, Catalog: &recipes.Catalog{Recipes: []recipes.Recipe{r}}, MinBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Opportunities) != 1 || res.Opportunities[0].Target != dd {
		t.Fatalf("opportunities = %v; warnings %v", ids(res.Opportunities), res.Warnings)
	}
	return res.Opportunities[0]
}

// A group with a member outside the folder keeps all its data: the range is
// exact and equals the suggestion's size.
func TestMeasureGroupWithAMemberOutsideAddsNothing(t *testing.T) {
	home, root, dd := folder(t, func(dd, home string) {
		cp(t, filepath.Join(home, "ref/shared.bin"), filepath.Join(dd, "a.bin"))
		cp(t, filepath.Join(dd, "a.bin"), filepath.Join(dd, "b.bin"))
	})
	op := suggestion(t, home, root, dd)
	sp, err := engine.Measure(context.Background(), engine.OpportunityOptions{Home: home}, op)
	if err != nil {
		t.Fatal(err)
	}
	// own.bin frees 2 MB. a.bin and b.bin are clones of ref/shared.bin, which
	// stays outside: the group has a member outside, so none of it frees.
	if sp == nil || sp.Upper == nil || sp.Lower != op.Bytes.Value || *sp.Upper != sp.Lower {
		t.Errorf("span = %+v; want lower = upper = %d (only own.bin frees)", sp, op.Bytes.Value)
	}
}

// Two files that are clones of each other and of nothing outside: the
// suggestion size cannot see them, the range counts one copy.
func TestMeasureWholeGroupInsideRaisesTheUpperEnd(t *testing.T) {
	home, root, dd := folder(t, func(dd, _ string) {
		fstree.Build(t, dd, fstree.File("g.bin", 4<<20))
		cp(t, filepath.Join(dd, "g.bin"), filepath.Join(dd, "g1.bin"))
	})
	op := suggestion(t, home, root, dd)
	sp, err := engine.Measure(context.Background(), engine.OpportunityOptions{Home: home}, op)
	if err != nil {
		t.Fatal(err)
	}
	if sp == nil || sp.Upper == nil {
		t.Fatalf("span = %+v; want a range", sp)
	}
	if sp.Lower != op.Bytes.Value {
		t.Errorf("lower %d; want the suggestion's size %d", sp.Lower, op.Bytes.Value)
	}
	if got := *sp.Upper - sp.Lower; got < 4<<20 || got > 5<<20 {
		t.Errorf("upper - lower = %d; want about one 4 MB copy", got)
	}
}

// A clone of a file that stays outside: lower and upper agree.
func TestMeasureCloneOfOutsideFileIsExact(t *testing.T) {
	home, root, dd := folder(t, func(dd, home string) {
		cp(t, filepath.Join(home, "ref/shared.bin"), filepath.Join(dd, "clone.bin"))
	})
	op := suggestion(t, home, root, dd)
	sp, err := engine.Measure(context.Background(), engine.OpportunityOptions{Home: home}, op)
	if err != nil {
		t.Fatal(err)
	}
	if sp == nil || sp.Upper == nil || *sp.Upper != sp.Lower || sp.Lower != op.Bytes.Value {
		t.Errorf("span = %+v; want lower = upper = %d", sp, op.Bytes.Value)
	}
}

// With nothing shared there is nothing to walk for.
func TestMeasureSkipsAFolderThatSharesNothing(t *testing.T) {
	home, root, dd := folder(t, func(string, string) {})
	op := suggestion(t, home, root, dd)
	if sp, err := engine.Measure(context.Background(), engine.OpportunityOptions{Home: home}, op); err != nil || sp != nil {
		t.Errorf("span = %+v, err = %v; want neither", sp, err)
	}
}

// Only a target the scan recorded is walked, and only for a measured
// quarantine suggestion.
func TestMeasureWalksOnlyWhatTheScanRecorded(t *testing.T) {
	home, root, dd := folder(t, func(dd, home string) {
		cp(t, filepath.Join(home, "ref/shared.bin"), filepath.Join(dd, "clone.bin"))
	})
	op := suggestion(t, home, root, dd)
	opts := engine.OpportunityOptions{Home: home}
	cases := map[string]func(model.Opportunity) model.Opportunity{
		"a path the scan never stored": func(o model.Opportunity) model.Opportunity { o.Target = filepath.Join(home, "ref"); return o },
		"a sibling of the stored path": func(o model.Opportunity) model.Opportunity { o.Target += "x"; return o },
		"no target":                    func(o model.Opportunity) model.Opportunity { o.Target = ""; return o },
		"a native command":             func(o model.Opportunity) model.Opportunity { o.Method = model.MethodNative; return o },
		"advice only":                  func(o model.Opportunity) model.Opportunity { o.Method = model.MethodAdvise; return o },
		"a size from a tool": func(o model.Opportunity) model.Opportunity {
			o.Bytes.Source = model.SourceTool
			return o
		},
		"a size that is not this folder's": func(o model.Opportunity) model.Opportunity { o.Bytes.Value++; return o },
	}
	for name, mod := range cases {
		if sp, err := engine.Measure(context.Background(), opts, mod(op)); err != nil || sp != nil {
			t.Errorf("%s: span = %+v, err = %v; want neither", name, sp, err)
		}
	}
}

func TestMeasureStopsWhenCanceled(t *testing.T) {
	home, root, dd := folder(t, func(dd, home string) {
		cp(t, filepath.Join(home, "ref/shared.bin"), filepath.Join(dd, "clone.bin"))
	})
	op := suggestion(t, home, root, dd)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sp, err := engine.Measure(ctx, engine.OpportunityOptions{Home: home}, op); !errors.Is(err, context.Canceled) || sp != nil {
		t.Errorf("span = %+v, err = %v; want context.Canceled", sp, err)
	}
}

func TestSuspected(t *testing.T) {
	p := func(v int64) *int64 { return &v }
	const mb, gb = 1 << 20, 1 << 30
	cases := []struct {
		name string
		n    model.Node
		want bool
	}{
		{"unknown private", model.Node{Bytes: 10 * gb}, false},
		{"nothing shared", model.Node{Bytes: 10 * gb, Private: p(10 * gb)}, false},
		{"under 1 percent", model.Node{Bytes: 10 * gb, Private: p(10*gb - 50*mb)}, false},
		{"under 1 MB", model.Node{Bytes: 50 * mb, Private: p(50*mb - mb/2)}, false},
		{"exactly 1 MB", model.Node{Bytes: 50 * mb, Private: p(49 * mb)}, false},
		{"over both", model.Node{Bytes: 10 * gb, Private: p(9 * gb)}, true},
		{"fully shared", model.Node{Bytes: 4 * mb, Private: p(0)}, true},
	}
	for _, c := range cases {
		if got := engine.Suspected(c.n); got != c.want {
			t.Errorf("%s: Suspected = %v; want %v", c.name, got, c.want)
		}
	}
}
