package engine

import (
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
)

// ScanNodes adapts one stored scan to recipes.NodeSource. A value made by
// NewScanNodes answers from an in-memory index of the scan; a bare literal
// asks the store every time.
type ScanNodes struct {
	St     *store.Store
	ScanID string

	ix *nodeIndex // shared by copies; nil: ask the store
}

var _ recipes.NodeSource = ScanNodes{}

// NewScanNodes returns a ScanNodes that reads the scan's kept nodes once,
// on its first question, and answers every later one from memory with the
// same results as the store. A scan with more than MaxIndexedNodes nodes is
// answered from the store instead. It is safe for concurrent use.
func NewScanNodes(st *store.Store, scanID string) ScanNodes {
	return ScanNodes{St: st, ScanID: scanID, ix: newNodeIndex(st, scanID)}
}

// Nodes implements recipes.NodeSource.
func (s ScanNodes) Nodes(prefix string, maxDepth int) ([]model.Node, error) {
	return s.Query(store.NodeQuery{Prefix: prefix, MaxDepth: maxDepth})
}

// Query returns what store.Nodes returns for q, from memory when the scan
// has an index.
func (s ScanNodes) Query(q store.NodeQuery) ([]model.Node, error) {
	if s.ix != nil {
		if ns, ok := s.ix.query(q); ok {
			return ns, nil
		}
	}
	return s.St.Nodes(s.ScanID, q)
}
