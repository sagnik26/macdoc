package engine

import (
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
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
