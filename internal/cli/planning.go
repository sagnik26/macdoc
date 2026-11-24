package cli

import (
	"fmt"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// NowFunc is the dashboard's clock (tests fix it).
var NowFunc = time.Now

// MarkedSelector plans the marked suggestions in ranked order. It returns nil (with a
// flash) if nothing is marked.
func (m *TuiModel) MarkedSelector() *planner.Selector {
	var ids []string
	for _, op := range m.Items() {
		if m.Marked[op.ID] {
			ids = append(ids, op.ID)
		}
	}
	if len(ids) == 0 {
		m.Flash = "Nothing is ticked."
		return nil
	}
	return &planner.Selector{IDs: ids}
}

// markedTotal is the number of marked items and their known bytes.
func (m TuiModel) markedTotal() (int, int64) {
	n, sum := 0, int64(0)
	for _, op := range m.Items() {
		if m.Marked[op.ID] {
			n++
			if op.Bytes.Source != model.SourceUnknown {
				sum += op.Bytes.Value
			}
		}
	}
	return n, sum
}

// markedNote is the /clean list's summary of what is ticked: "" if nothing is.
func (m TuiModel) markedNote() string {
	n, sum := 0, int64(0)
	for _, op := range m.chooseItems() {
		if m.Marked[op.ID] {
			n++
			if op.Bytes.Source != model.SourceUnknown {
				sum += op.Bytes.Value
			}
		}
	}
	if n == 0 {
		return ""
	}
	return m.t.accent.Render(fmt.Sprintf("%d ticked · %s", n, engine.DecimalBytes(sum)))
}
