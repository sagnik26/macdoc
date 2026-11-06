package tests

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
)

// A prefix query is a range on the primary key. These tests pin what the range
// includes and excludes, using names that sit on both sides of its edges, and
// guard the speed that made the range worth having.

func loadNodes(t *testing.T, ps ...string) (*store.Store, string) {
	t.Helper()
	s := openTemp(t)
	id := mustBegin(t, s, t0)
	var ns []model.Node
	for i, p := range ps {
		ns = append(ns, dir(p, int64(1000+i)))
	}
	if err := s.AddNodes(id, ns); err != nil {
		t.Fatal(err)
	}
	mustFinish(t, s, id, model.ScanComplete, t0)
	return s, id
}

func TestNodesPrefixIsComponentWise(t *testing.T) {
	all := []string{
		"/a/b", "/a/b/c", "/a/b/c/d", "/A/B/E", // inside "/a/b", whatever the case
		"/a/bc", "/a/b0", "/a/b.x", "/a/b+x", "/a/b-x", "/a/b_x", "/a/b%x", // siblings that start the same way
		"/a/b ", "/a/B1", "/a", "/a/a", "/a/c", // around it
	}
	s, id := loadNodes(t, all...)
	for _, tt := range []struct {
		prefix string
		want   []string
	}{
		{"/a/b", []string{"/a/b", "/a/b/c", "/a/b/c/d", "/A/B/E"}},
		{"/A/B", []string{"/a/b", "/a/b/c", "/a/b/c/d", "/A/B/E"}}, // case does not matter
		{"/a/b/", []string{"/a/b", "/a/b/c", "/a/b/c/d", "/A/B/E"}},
		{"/a/b/c", []string{"/a/b/c", "/a/b/c/d"}},
		{"/a/b_x", []string{"/a/b_x"}}, // "_" is a character, not a wildcard
		{"/a/b%x", []string{"/a/b%x"}}, // "%" is a character, not a wildcard
		{"/a/bc", []string{"/a/bc"}},
		{"/a/zzz", nil},
	} {
		ns, err := s.Nodes(id, store.NodeQuery{Prefix: tt.prefix})
		if err != nil {
			t.Fatal(err)
		}
		got := paths(ns)
		slices.Sort(got)
		want := slices.Clone(tt.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("prefix %q = %v, want %v", tt.prefix, got, want)
		}
	}
}

func TestNodesPrefixWithDepthSizeAndLimit(t *testing.T) {
	s, id := loadNodes(t, "/p", "/p/a", "/p/a/b", "/p/a/b/c", "/p/z", "/q/a")
	// depth is relative to the prefix: 1 is its children only
	ns, err := s.Nodes(id, store.NodeQuery{Prefix: "/p", MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := paths(ns)
	slices.Sort(got)
	if want := []string{"/p", "/p/a", "/p/z"}; !slices.Equal(got, want) {
		t.Errorf("depth 1 = %v, want %v", got, want)
	}
	// sizes are 1000+index, so MinBytes drops the smaller ones, and the
	// result stays biggest first
	ns, err = s.Nodes(id, store.NodeQuery{Prefix: "/p", MinBytes: 1002})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(ns); !slices.Equal(got, []string{"/p/z", "/p/a/b/c", "/p/a/b"}) {
		t.Errorf("min bytes = %v", got)
	}
	ns, err = s.Nodes(id, store.NodeQuery{Prefix: "/p", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(ns); !slices.Equal(got, []string{"/p/z", "/p/a/b/c"}) {
		t.Errorf("limit 2 = %v", got)
	}
	// the root prefix means everything
	ns, err = s.Nodes(id, store.NodeQuery{Prefix: "/"})
	if err != nil || len(ns) != 6 {
		t.Errorf("root prefix = %d nodes, %v; want 6", len(ns), err)
	}
}

// Detectors ask for the children of many folders in one scan. Reading every
// node of the scan for each question made that quadratic: 237 ms per question
// at 300,000 nodes (the primary-key range takes 0.2 ms). The bound is far above
// the fast time and far below the slow one so that it fails on the regression
// and not on a slow machine.
func TestNodesPrefixDoesNotReadTheWholeScan(t *testing.T) {
	if testing.Short() {
		t.Skip("builds 60,000 nodes")
	}
	s := openTemp(t)
	id := mustBegin(t, s, t0)
	batch := make([]model.Node, 0, 1000)
	flush := func() {
		if err := s.AddNodes(id, batch); err != nil {
			t.Fatal(err)
		}
		batch = batch[:0]
	}
	for p := 0; p < 600; p++ {
		for d := 0; d < 100; d++ {
			batch = append(batch, dir(fmt.Sprintf("/Users/u/work/proj%04d/mod%03d/sub%03d", p, d/10, d%10), int64(1000+d)))
			if len(batch) == cap(batch) {
				flush()
			}
		}
	}
	flush()
	mustFinish(t, s, id, model.ScanComplete, t0)

	const calls = 100
	start := time.Now()
	for i := 0; i < calls; i++ {
		ns, err := s.Nodes(id, store.NodeQuery{Prefix: fmt.Sprintf("/Users/u/work/proj%04d", (i*7)%600), MaxDepth: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(ns) == 0 {
			t.Fatal("a prefix inside the scan returned nothing")
		}
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("%d prefix queries on 60,000 nodes took %v; the primary-key range should take milliseconds", calls, took)
	}
}
