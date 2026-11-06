package tests

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"

	"github.com/sagnik26/macdoc/internal/pathfold"
)

const (
	gB = int64(1_000_000_000)
	// gKeepFloor mirrors engine.KeptFloor.
	gKeepFloor = engine.KeptFloor
)

// growTree builds before/after scans of a random nested directory tree with
// about n nodes under /Users/me, like a real home folder: leaves grow
// (mostly by more than the 500 MB listing threshold, some by little) and
// every folder above a leaf grows by the sum of what is below it. Maps are
// keyed by lower-cased path, as the engine builds them.
func growTree(n int, seed uint64) (before, after map[string]model.Node) {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // G404: a seeded generator makes the trees reproducible
	paths := []string{"/Users/me"}
	depth := []int{2}
	parent := []int{-1}
	for len(paths) < n {
		// Prefer recent nodes so that trees get deep, not just wide.
		p := len(paths) - 1 - r.IntN(min(len(paths), 12))
		if r.IntN(4) == 0 {
			p = r.IntN(len(paths))
		}
		if depth[p] >= 9 {
			continue
		}
		name := fmt.Sprintf("Dir%d", len(paths))
		if r.IntN(3) == 0 {
			name = strings.ToLower(name)
		}
		paths = append(paths, paths[p]+"/"+name)
		depth = append(depth, depth[p]+1)
		parent = append(parent, p)
	}
	own := make([]int64, len(paths))
	hasKid := make([]bool, len(paths))
	for i := 1; i < len(paths); i++ {
		hasKid[parent[i]] = true
	}
	for i := range paths {
		switch {
		case !hasKid[i] && r.IntN(10) < 6:
			own[i] = 600_000_000 + r.Int64N(3*gB)
		case !hasKid[i]:
			own[i] = r.Int64N(100_000_000)
		case r.IntN(10) == 0:
			own[i] = r.Int64N(gB)
		}
	}
	grow := append([]int64(nil), own...)
	for i := len(paths) - 1; i > 0; i-- {
		grow[parent[i]] += grow[i]
	}
	before = make(map[string]model.Node, len(paths))
	after = make(map[string]model.Node, len(paths))
	for i, p := range paths {
		b := 200_000_000 + r.Int64N(5*gB)
		k := pathfold.Fold(p)
		if r.IntN(15) != 0 { // some folders are new: under the floor before
			before[k] = model.Node{Path: p, Kind: model.NodeDir, Bytes: b}
		} else {
			b = gKeepFloor / 2
		}
		after[k] = model.Node{Path: p, Kind: model.NodeDir, Bytes: b + grow[i]}
	}
	return before, after
}

var growSink []engine.TrendFolder

func benchGrowing(b *testing.B, f func(before, after map[string]model.Node) []engine.TrendFolder) {
	for _, size := range []struct {
		n    int
		seed uint64
	}{{75, 75}, {750, 750}, {7500, 7500}} {
		before, after := growTree(size.n, size.seed)
		cands := 0
		for k, a := range after {
			if a.Bytes-before[k].Bytes >= 500_000_000 {
				cands++
			}
		}
		b.Run(fmt.Sprintf("K=%d", cands), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				growSink = f(before, after)
			}
		})
	}
}

func BenchmarkGrowing(b *testing.B) { benchGrowing(b, engine.Growing) }
