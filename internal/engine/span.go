package engine

import (
	"context"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/pathfold"
	"github.com/sagnik26/macdoc/internal/scanner"
	"github.com/sagnik26/macdoc/internal/store"
)

// sharePercent sets the relative part of the share test: the gap between
// allocated and certain-to-free bytes must exceed 1/sharePercent of the
// allocation.
const sharePercent = 100

// ShareFloor is the absolute part of the share test: the gap must exceed this
// many bytes (1 MB). Below both limits the lower and upper bounds cannot
// differ by enough to matter, so no walk is made.
const ShareFloor = 1 << 20

// MeasureTimeout is the longest the on-demand walk of one folder may take.
const MeasureTimeout = 3 * time.Minute

// Span is what removing a suggestion frees: at least Lower and, when Upper
// is not nil, at most *Upper. A nil Upper is unknown, never zero.
type Span struct {
	Lower int64
	Upper *int64
}

// Suspected reports whether a scan node looks like it shares space with
// copies of its files: its private size is known and well under its
// allocation. A node with an unknown private size is not suspected; its
// suggestion is sized by its allocation and says nothing about sharing.
func Suspected(n model.Node) bool {
	if n.Private == nil {
		return false
	}
	gap := n.Bytes - *n.Private
	return gap > ShareFloor && gap > n.Bytes/sharePercent
}

// Measurable reports whether a range could apply to op at all: it is a
// measured quarantine folder with a target. It reads nothing from disk, so a
// caller can use it to decide whether to start Measure; Measure itself still
// checks the stored scan before it walks anything.
func Measurable(op model.Opportunity) bool {
	return op.Method == model.MethodQuarantine && op.Bytes.Source == model.SourceMeasured && op.Target != ""
}

// Measure measures, on demand, the range of space removing op frees. It
// returns nil when there is nothing to add: the suggestion is not a measured
// folder from the scan, its files do not look shared, the walk found files
// the kernel gave no private size for, or the folder is gone. It walks only
// op.Target and only when a stored node of op.ScanID has that exact path, so
// a path the scan never recorded is never read. Canceling ctx stops the
// walk and returns its error.
func Measure(ctx context.Context, o OpportunityOptions, op model.Opportunity) (*Span, error) {
	if !Measurable(op) {
		return nil, nil
	}
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	st, err := store.Open(o.StorePath)
	if err != nil {
		return nil, err
	}
	nodes, err := NewScanNodes(st, op.ScanID).Query(store.NodeQuery{Prefix: op.Target, Limit: 1})
	_ = st.Close()
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 || pathfold.Fold(nodes[0].Path) != pathfold.Fold(op.Target) || !Suspected(nodes[0]) {
		return nil, nil
	}
	if nodes[0].Reclaimable() != op.Bytes.Value {
		return nil, nil // the suggestion's size is not this folder's alone
	}
	ctx, cancel := context.WithTimeout(ctx, MeasureTimeout)
	defer cancel()
	b, err := scanner.Measure(ctx, op.Target, 0)
	if err != nil || b.Lacking > 0 {
		return nil, err
	}
	sp := &Span{Lower: b.Lower}
	if b.Bounded {
		u := b.Upper
		sp.Upper = &u
	}
	return sp, nil
}
