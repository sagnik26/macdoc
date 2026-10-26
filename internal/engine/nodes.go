package engine

import (
	"macdoc/internal/model"
	"macdoc/internal/recipes"
	"macdoc/internal/store"
)

// scanNodes adapts one stored scan to recipes.NodeSource.
type scanNodes struct {
	st     *store.Store
	scanID string
}

var _ recipes.NodeSource = scanNodes{}

// Nodes implements recipes.NodeSource.
func (s scanNodes) Nodes(prefix string, maxDepth int) ([]model.Node, error) {
	return s.st.Nodes(s.scanID, store.NodeQuery{Prefix: prefix, MaxDepth: maxDepth})
}
