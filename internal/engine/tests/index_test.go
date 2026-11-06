package tests

import (
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
)

// The in-memory index must answer every question exactly as store.Nodes
// does. These tests compare the two on random trees whose names sit on the
// edges of the prefix range, then check the fallback and concurrent reads.

// idxNames are the path components random trees are built from. They share
// prefixes (b, bc, b0, b.x), hold LIKE wildcards (% and _), differ only by
// case or by Unicode normalization (é composed and decomposed), and fold to
// the same name only under full case folding (ß and SS).
var idxNames = []string{
	"a", "A", "b", "B", "bc", "b0", "b.x", "b+x", "b-x", "b_x", "b%x", "b ", "b/",
	"c", "caf\u00e9", "cafe\u0301", "CAF\u00c9", "stra\u00dfe", "STRASSE", "strasse",
	"\u0130", "i\u0307", "x y", "\u00e9", "e\u0301", "1", "10", "2", "_", "%", "..x", "\xff\xfe",
}

func idxTree(r *rand.Rand) []model.Node {
	n := 5 + r.Intn(120)
	seen := map[string]bool{}
	var out []model.Node
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		kind := model.NodeDir
		if r.Intn(5) == 0 {
			kind = model.NodeFile
		}
		// few distinct sizes, so equal sizes (the path tie-break) are common
		nd := model.Node{
			Path: p, Kind: kind, Bytes: int64(r.Intn(6)) * 1000, Files: int64(r.Intn(9)),
			MtimeMax: int64(r.Intn(1000)), Depth: strings.Count(p, "/"),
		}
		// The private size must reach the index as the store holds it:
		// unknown stays unknown, and a known zero stays zero.
		if r.Intn(3) != 0 {
			v := int64(r.Intn(3)) * 500
			nd.Private = &v
		}
		out = append(out, nd)
	}
	if r.Intn(4) == 0 {
		add("/")
	}
	for range n {
		parts := make([]string, 1+r.Intn(5))
		for i := range parts {
			parts[i] = idxNames[r.Intn(len(idxNames))]
			parts[i] = strings.ReplaceAll(parts[i], "/", "") // a name has no slash
			if parts[i] == "" {
				parts[i] = "z"
			}
		}
		// keep ancestors too, like a real scan, but not always
		for i := 1; i <= len(parts); i++ {
			if i == len(parts) || r.Intn(2) == 0 {
				add("/" + strings.Join(parts[:i], "/"))
			}
		}
	}
	return out
}

func idxMutate(r *rand.Rand, p string) string {
	switch r.Intn(9) {
	case 0:
		return strings.ToUpper(p)
	case 1:
		return strings.ToLower(p)
	case 2:
		return p + "/"
	case 3:
		return p + "//"
	case 4:
		return strings.ReplaceAll(p, "\u00e9", "e\u0301") // NFC to NFD
	case 5:
		return strings.ReplaceAll(p, "e\u0301", "\u00e9") // NFD to NFC
	case 6:
		return p + "/./x/.."
	case 7:
		return strings.Replace(p, "/", "//", 1)
	}
	return p
}

func idxQuery(r *rand.Rand, nodes []model.Node) store.Query {
	var q store.Query
	switch r.Intn(12) {
	case 0:
		q.Prefix = ""
	case 1:
		q.Prefix = "/"
	case 2:
		q.Prefix = "//"
	case 3:
		q.Prefix = "/" + idxNames[r.Intn(len(idxNames))] // maybe absent
	case 4:
		q.Prefix = "relative/" + idxNames[r.Intn(len(idxNames))]
	default:
		q.Prefix = idxMutate(r, nodes[r.Intn(len(nodes))].Path)
	}
	q.MaxDepth = []int{-1, 0, 0, 1, 1, 2, 3, 6}[r.Intn(8)]
	q.MinBytes = []int64{-5, 0, 0, 1000, 3000, 5000, 6000, 1 << 40}[r.Intn(8)]
	q.Limit = []int{0, 0, 0, 1, 2, 5, 100, -1}[r.Intn(8)]
	return q
}

func idxStore(t *testing.T, nodes []model.Node) (*store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	id, err := st.BeginScan("/", "test", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddNodes(id, nodes); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishScan(id, model.ScanComplete, 0, 0, at); err != nil {
		t.Fatal(err)
	}
	return st, id
}

// idxRand is a seeded generator, so a failing tree can be rebuilt from its seed.
func idxRand(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed)) //nolint:gosec // test data, not a secret
}

// idxCompare asks the store, then the index, the same random queries. The
// store is closed after the index's first answer, so every later answer
// must come from memory. It returns how many queries it ran and how many
// returned nodes.
func idxCompare(t *testing.T, seed int64, perTree int) (ran, nonEmpty int) {
	t.Helper()
	r := idxRand(seed)
	nodes := idxTree(r)
	st, id := idxStore(t, nodes)
	qs := make([]store.Query, perTree)
	want := make([][]model.Node, perTree)
	for i := range qs {
		qs[i] = idxQuery(r, nodes)
		var err error
		if want[i], err = st.Nodes(id, qs[i]); err != nil {
			t.Fatalf("tree %d: store %+v: %v", seed, qs[i], err)
		}
	}
	sn := engine.NewScanNodes(st, id)
	for i, q := range qs {
		if i == 1 {
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
		}
		got, err := sn.Query(q)
		if err != nil {
			t.Fatalf("tree %d: index %+v: %v", seed, q, err)
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Fatalf("tree %d, query %+v:\n got  %v\n want %v", seed, q, got, want[i])
		}
		if len(got) > 0 {
			nonEmpty++
		}
	}
	return len(qs), nonEmpty
}

func TestIndexMatchesStore(t *testing.T) {
	const trees, perTree = 24, 250 // 6,000 queries
	total, nonEmpty := 0, 0
	for seed := range trees {
		ran, ne := idxCompare(t, int64(seed)+1, perTree)
		total += ran
		nonEmpty += ne
	}
	if total < 5000 {
		t.Fatalf("only %d queries ran", total)
	}
	// A differential test that mostly compares empty results proves little.
	if nonEmpty < total/3 {
		t.Errorf("only %d of %d queries returned nodes", nonEmpty, total)
	}
	t.Logf("%d queries over %d trees matched the store (%d returned nodes)", total, trees, nonEmpty)
}

func TestIndexNodesAdapter(t *testing.T) {
	r := idxRand(99)
	nodes := idxTree(r)
	st, id := idxStore(t, nodes)
	sn := engine.NewScanNodes(st, id)
	for range 200 {
		q := idxQuery(r, nodes)
		got, err := sn.Nodes(q.Prefix, q.MaxDepth)
		if err != nil {
			t.Fatal(err)
		}
		want, err := st.Nodes(id, store.Query{Prefix: q.Prefix, MaxDepth: q.MaxDepth})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Nodes(%q, %d) = %v, want %v", q.Prefix, q.MaxDepth, got, want)
		}
	}
}

func TestIndexResultIsACopy(t *testing.T) {
	st, id := idxStore(t, []model.Node{
		{Path: "/a", Kind: model.NodeDir, Bytes: 5}, {Path: "/a/b", Kind: model.NodeDir, Bytes: 3},
	})
	sn := engine.NewScanNodes(st, id)
	first, _ := sn.Nodes("/a", 0)
	first[0].Bytes = 999
	first[0].Path = "/changed"
	again, _ := sn.Nodes("/a", 0)
	if again[0].Path != "/a" || again[0].Bytes != 5 {
		t.Errorf("a caller's change reached the index: %+v", again[0])
	}
}

func TestIndexBigScanUsesTheStore(t *testing.T) {
	old := engine.MaxIndexedNodes
	engine.MaxIndexedNodes = 3
	t.Cleanup(func() { engine.MaxIndexedNodes = old })
	var nodes []model.Node
	for i := range 5 {
		nodes = append(nodes, model.Node{Path: fmt.Sprintf("/a/%d", i), Kind: model.NodeDir, Bytes: int64(i)})
	}
	st, id := idxStore(t, nodes)
	sn := engine.NewScanNodes(st, id)
	got, err := sn.Nodes("/a", 0)
	if err != nil || len(got) != 5 {
		t.Fatalf("over the limit: %v, %v", got, err)
	}
	// Proof that it asked the store: with the database closed it fails.
	_ = st.Close()
	if _, err := sn.Nodes("/a", 0); err == nil {
		t.Error("a scan over MaxIndexedNodes was held in memory")
	}
}

func TestIndexUnknownScanFailsLikeTheStore(t *testing.T) {
	st, _ := idxStore(t, []model.Node{{Path: "/a", Kind: model.NodeDir, Bytes: 1}})
	_, err := engine.NewScanNodes(st, "S-nothere").Nodes("/a", 0)
	if !errors.Is(err, store.ErrNoScan) {
		t.Errorf("err = %v, want ErrNoScan", err)
	}
}

func TestIndexConcurrentReads(t *testing.T) {
	r := idxRand(7)
	nodes := idxTree(r)
	st, id := idxStore(t, nodes)
	qs := make([]store.Query, 64)
	want := make([][]model.Node, len(qs))
	for i := range qs {
		qs[i] = idxQuery(r, nodes)
		want[i], _ = st.Nodes(id, qs[i])
	}
	sn := engine.NewScanNodes(st, id) // not loaded yet: the workers race to load it
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for w := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range 200 {
				i := (w + k) % len(qs)
				got, err := sn.Query(qs[i])
				if err != nil || !reflect.DeepEqual(got, want[i]) {
					select {
					case errs <- fmt.Sprintf("query %+v: %v, %v", qs[i], got, err):
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
