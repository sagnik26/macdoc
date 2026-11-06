package tests

import (
	"maps"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"

	"github.com/sagnik26/macdoc/internal/pathfold"
)

// growPool holds name pieces that differ only by case or by Unicode form, so
// that generated paths collide the way real ones can: "é" composed and
// decomposed, the Kelvin sign (which lower-cases to "k"), dotted capital I.
var growPool = []string{"a", "A", "b", "B", "k", "K", "K", "é", "é", "É", "Ä", "ä", "ß", "İ", "x y", "a-b", "a.b"}

// growCase builds a random pair of scans: a small tree of folders (sometimes
// including "/" and files), mostly present in "before" at some size, with
// sizes in 100 MB steps so that equal sizes and the 80% boundary come up.
func growCase(r *rand.Rand) (before, after map[string]model.Node) {
	const step = int64(100_000_000)
	pool := growPool[:3+r.IntN(len(growPool)-2)]
	seg := func() string { return pool[r.IntN(len(pool))] }
	paths := map[string]string{} // key → path
	add := func(p string) { paths[pathfold.Fold(p)] = p }
	if r.IntN(6) == 0 {
		add("/")
	}
	for range 1 + r.IntN([]int{4, 10, 30}[r.IntN(3)]) {
		lead := "/"
		if r.IntN(8) == 0 {
			lead = "" // a relative path, which a scan never stores but the rules still define
		}
		parts := make([]string, 1+r.IntN(5))
		for i := range parts {
			parts[i] = seg()
		}
		for i := 1; i <= len(parts); i++ {
			if i == len(parts) || r.IntN(10) < 7 { // usually keep the ancestors too
				add(lead + strings.Join(parts[:i], "/"))
			}
		}
	}
	peak := 2 + r.IntN(8)                   // how big single growths get, in steps
	keys := slices.Sorted(maps.Keys(paths)) // fixed order, so seeds reproduce
	// A folder's growth includes its children's, as in real scans (mostly).
	total := map[string]int64{}
	for _, k := range keys {
		if r.IntN(3) == 0 {
			continue
		}
		g := step * int64(r.IntN(peak))
		for a := k; ; {
			total[a] += g
			i := strings.LastIndexByte(a, '/')
			if i < 0 {
				break
			}
			if i == 0 {
				if a != "/" {
					total["/"] += g
				}
				break
			}
			a = a[:i]
		}
	}
	before = map[string]model.Node{}
	after = map[string]model.Node{}
	for _, k := range keys {
		p := paths[k]
		b := step * int64(r.IntN(30))
		g := total[k]
		if r.IntN(4) == 0 { // not a clean sum
			g = max(0, g+step*int64(r.IntN(9)-4))
		}
		kind := model.NodeDir
		if r.IntN(12) == 0 {
			kind = model.NodeFile
		}
		if r.IntN(5) != 0 {
			before[k] = model.Node{Path: p, Kind: kind, Bytes: b}
		} else {
			b = 0
		}
		after[k] = model.Node{Path: p, Kind: kind, Bytes: b + g}
	}
	return before, after
}

// TestGrowingMatchesReference checks the fast Growing against the original
// implementation (oracle_test.go) on random inputs.
func TestGrowingMatchesReference(t *testing.T) {
	n := 20_000
	if v, err := strconv.Atoi(os.Getenv("MACDOC_GROWING_CASES")); err == nil {
		n = v // e.g. 100000 for the full proof, which takes about 5 s without -race
	}
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: a seeded generator makes the cases reproducible
	var nonEmpty, several, capped int
	for i := range n {
		var before, after map[string]model.Node
		if i%500 == 499 { // some realistic, larger trees
			before, after = growTree(20+r.IntN(300), r.Uint64())
		} else {
			before, after = growCase(r)
		}
		want := oracleGrowing(before, after)
		got := engine.Growing(before, after)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d differs\nbefore: %v\nafter:  %v\ngot:  %+v\nwant: %+v", i, before, after, got, want)
		}
		if len(want) > 0 {
			nonEmpty++
		}
		if len(want) > 1 {
			several++
		}
		if len(want) == 8 {
			capped++
		}
	}
	t.Logf("%d cases, all identical (%d non-empty, %d with 2+ folders, %d at the cap of 8)", n, nonEmpty, several, capped)
}
