package engine

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/pathfold"
	"github.com/sagnik26/macdoc/internal/store"
)

// MaxIndexedNodes is the most kept nodes a scan may have for its nodes to be
// held in memory (a variable so tests can lower it). A node costs about 310
// bytes once loaded and about 540 while loading (measured with 300,000
// nodes), so at this bound the index is about 16 MB, 27 MB at its peak. That
// keeps a read-only command near the project's goal of under 15 MB for
// commands that use cached state (docs/DEVELOPMENT_PLAN.md). A bigger scan is
// answered from the store instead, query by query. Nodes are kept above 100 MB
// or for recipe roots, so real scans are expected to stay far below it.
var MaxIndexedNodes = 50_000

// nodeIndex answers store.NodeQuery questions for one scan from memory. The
// scan's kept nodes are read once, on the first question, and sorted by their
// folded path, so everything at or below a folder is one contiguous range.
// After that first load the index is never written, so reads need no lock.
type nodeIndex struct {
	st   *store.Store
	id   string
	once sync.Once

	entries []entry // sorted by fold; empty unless ok
	ok      bool    // false: too big or unreadable, use the store
}

// entry is one node with the keys the store compares on.
type entry struct {
	fold  string // pathfold.Fold of the path: the store's path_fold column
	depth int    // components of the path: the store's depth column
	node  model.Node
}

func newNodeIndex(st *store.Store, id string) *nodeIndex {
	return &nodeIndex{st: st, id: id}
}

// load reads the scan's nodes. Any failure leaves ok false, so the caller
// asks the store, which reports the same error itself.
func (x *nodeIndex) load() {
	var n int
	row := x.st.DB.QueryRow(`SELECT COUNT(*) FROM nodes WHERE scan_id = ?`, x.id)
	if row.Scan(&n) != nil || n > MaxIndexedNodes {
		return
	}
	ns, err := x.st.Nodes(x.id, store.Query{})
	if err != nil {
		return
	}
	kinds := map[model.Kind]model.Kind{} // one string per kind, not one per node
	es := make([]entry, len(ns))
	for i, nd := range ns {
		k, seen := kinds[nd.Kind]
		if !seen {
			k = nd.Kind
			kinds[k] = k
		}
		nd.Kind = k
		es[i] = entry{fold: pathfold.Fold(nd.Path), depth: store.DepthOf(nd.Path), node: nd}
	}
	slices.SortFunc(es, func(a, b entry) int { return strings.Compare(a.fold, b.fold) })
	x.entries, x.ok = es, true
}

// query answers q exactly as store.Nodes does. ok is false when the index is
// not available and the caller must ask the store.
func (x *nodeIndex) query(q store.Query) (out []model.Node, ok bool) {
	x.once.Do(x.load)
	if !x.ok {
		return nil, false
	}
	prefix, prefixDepth := "", 0
	if q.Prefix != "" {
		if p := pathfold.Fold(path.Clean(q.Prefix)); p != "/" {
			prefix, prefixDepth = p, store.DepthOf(p)
		}
	}
	maxDepth := -1
	if q.MaxDepth > 0 {
		maxDepth = prefixDepth + q.MaxDepth
	}
	out = []model.Node{}
	take := func(es []entry) {
		for i := range es {
			e := &es[i]
			if e.node.Bytes >= q.MinBytes && (maxDepth < 0 || e.depth <= maxDepth) {
				out = append(out, e.node)
			}
		}
	}
	if prefix == "" {
		take(x.entries)
	} else {
		// The folder itself, then everything from "prefix/" up to, not
		// including, "prefix0" ("0" is the byte after "/"): the same two
		// ranges the store's SQL uses on its primary key.
		if i, found := x.find(prefix); found {
			take(x.entries[i : i+1])
		}
		lo, _ := x.find(prefix + "/")
		hi, _ := x.find(prefix + "0")
		take(x.entries[lo:hi])
	}
	slices.SortFunc(out, func(a, b model.Node) int {
		if c := cmp.Compare(b.Bytes, a.Bytes); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, true
}

// find returns the position of the first entry whose fold is at least key.
func (x *nodeIndex) find(key string) (int, bool) {
	return slices.BinarySearchFunc(x.entries, key, func(e entry, k string) int {
		return strings.Compare(e.fold, k)
	})
}
