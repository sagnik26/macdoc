package tests

import (
	"reflect"
	"testing"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"

	"github.com/sagnik26/macdoc/internal/pathfold"
)

// growScans builds scans from path → {before, after} sizes in MB, all
// folders. A before size of -1 means the folder is absent from the first scan.
func growScans(sizes map[string][2]int64) (before, after map[string]model.Node) {
	before, after = map[string]model.Node{}, map[string]model.Node{}
	for p, s := range sizes {
		k := pathfold.Fold(p)
		if s[0] >= 0 {
			before[k] = model.Node{Path: p, Kind: model.NodeDir, Bytes: s[0] * 1_000_000}
		}
		after[k] = model.Node{Path: p, Kind: model.NodeDir, Bytes: s[1] * 1_000_000}
	}
	return before, after
}

func TestGrowingRules(t *testing.T) {
	tests := []struct {
		name  string
		sizes map[string][2]int64
		want  []string
	}{
		{"parent explained by a child with 80% of its growth",
			map[string][2]int64{"/h": {1000, 2000}, "/h/a": {1000, 1800}}, []string{"/h/a"}},
		{"child below 80% but parent left with under growMin",
			map[string][2]int64{"/h": {1000, 2000}, "/h/a": {1000, 1700}}, []string{"/h/a"}},
		{"parent keeps its own growth beyond its child",
			map[string][2]int64{"/h": {1000, 3000}, "/h/a": {1000, 1700}}, []string{"/h", "/h/a"}},
		{"two children that together explain the parent",
			map[string][2]int64{"/h": {0, 1200}, "/h/a": {0, 600}, "/h/b": {0, 600}}, []string{"/h/a", "/h/b"}},
		{"a grandchild is subtracted once, through its kept parent",
			map[string][2]int64{"/h": {0, 2200}, "/h/a": {0, 1500}, "/h/a/b": {0, 600}}, []string{"/h", "/h/a", "/h/a/b"}},
		{"space that moved: the parent did not grow",
			map[string][2]int64{"/h": {1000, 1200}, "/h/new": {-1, 1000}}, []string{}},
		{"too small to list",
			map[string][2]int64{"/h": {1000, 1400}}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, after := growScans(tt.sizes)
			got := engine.Growing(before, after)
			paths := []string{}
			for _, f := range got {
				paths = append(paths, f.Path)
			}
			if !reflect.DeepEqual(paths, tt.want) {
				t.Errorf("got %v, want %v", paths, tt.want)
			}
			if ref := oracleGrowing(before, after); !reflect.DeepEqual(got, ref) {
				t.Errorf("differs from the reference: %+v", ref)
			}
		})
	}
}
