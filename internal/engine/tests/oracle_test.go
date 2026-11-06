package tests

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"

	"github.com/sagnik26/macdoc/internal/pathfold"
)

// This file keeps the original, straightforward (cubic) Growing as a
// reference. The fast version must give the same answer on every input; the
// differential test in growing_test.go checks that.

func oracleSizeIn(nodes map[string]model.Node, p string) engine.Range {
	if n, ok := nodes[pathfold.Fold(p)]; ok {
		return engine.Range{Low: n.Bytes, High: n.Bytes}
	}
	return engine.Range{Low: 0, High: engine.KeptFloor}
}

func oracleGrowing(before, after map[string]model.Node) []engine.TrendFolder {
	const growMin, growingMax = 500_000_000, 8
	type cand struct {
		engine.TrendFolder
		depth int
	}
	var cands []cand
	for _, n := range after {
		if n.Kind != model.NodeDir {
			continue
		}
		b := oracleSizeIn(before, n.Path)
		ch := engine.Range{Low: n.Bytes - b.High, High: n.Bytes - b.Low}
		if ch.Low < growMin {
			continue
		}
		if p, ok := after[pathfold.Fold(filepath.Dir(n.Path))]; ok && p.Path != n.Path {
			pb := oracleSizeIn(before, p.Path)
			if (p.Bytes-pb.High)*2 < ch.Low {
				continue
			}
		}
		cands = append(cands, cand{engine.TrendFolder{Path: n.Path, Before: b, After: n.Bytes, Change: ch}, strings.Count(n.Path, "/")})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].depth != cands[j].depth {
			return cands[i].depth > cands[j].depth
		}
		return cands[i].Path < cands[j].Path
	})
	var kept []cand
	for _, c := range cands {
		explained := false
		for _, k := range kept {
			if oracleUnder(k.Path, c.Path) && k.Change.Low*5 >= c.Change.Low*4 {
				explained = true
				break
			}
		}
		if !explained {
			kept = append(kept, c)
		}
	}
	paths := make([]string, len(kept))
	for i, c := range kept {
		paths[i] = c.Path
	}
	out := []engine.TrendFolder{}
	for i, c := range kept {
		rest := c.Change.Low
		for j, d := range kept[:i] {
			if oracleUnder(d.Path, c.Path) && !oracleNested(paths[:i], j, c.Path) {
				rest -= d.Change.Low
			}
		}
		if rest < growMin && rest < c.Change.Low {
			continue
		}
		out = append(out, c.TrendFolder)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Change.Low > out[j].Change.Low })
	if len(out) > growingMax {
		out = out[:growingMax]
	}
	return out
}

func oracleNested(kept []string, j int, dir string) bool {
	for k, o := range kept {
		if k != j && oracleUnder(o, dir) && oracleUnder(kept[j], o) {
			return true
		}
	}
	return false
}

func oracleUnder(p, dir string) bool {
	p, dir = pathfold.Fold(p), pathfold.Fold(dir)
	if dir == "/" {
		return p != "/"
	}
	return strings.HasPrefix(p, dir+"/")
}
