package engine

import (
	"macdoc/internal/model"
	"macdoc/internal/recipes"
	"macdoc/internal/store"
)

// ScanNodes adapts one stored scan to recipes.NodeSource.
type ScanNodes struct {
	St     *store.Store
	ScanID string
}

var _ recipes.NodeSource = ScanNodes{}

// Nodes implements recipes.NodeSource.
func (s ScanNodes) Nodes(prefix string, maxDepth int) ([]model.Node, error) {
	return s.St.Nodes(s.ScanID, store.NodeQuery{Prefix: prefix, MaxDepth: maxDepth})
}
