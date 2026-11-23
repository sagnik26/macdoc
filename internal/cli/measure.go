package cli

import (
	"context"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
)

// The dashboard shows the range of space a removal frees (ADR 0005, stage 3)
// for the suggestion in its detail pane. The walk can take seconds, so it runs
// as a command and never on the UI's thread. At most one walk runs at a time.
// A new selection cancels the walk before it, and a result that is not for the
// walk now in flight is dropped. Results are kept per suggestion until the next
// scan, so moving back does not walk again.

// SpanMsg is the result of one range walk. Seq says which walk it belongs to.
type SpanMsg struct {
	Seq  int
	ID   string
	Span *engine.Span
	Err  error
}

// spanState is shared by every copy of the model, like Marked. Only the
// Update thread touches its fields; the walking command touches only mu.
type spanState struct {
	mu     sync.Mutex // held for a whole walk, so a canceled one ends before the next starts
	seq    int        // number of the walk in flight, or the last one
	id     string     // suggestion being walked; empty when no walk is in flight
	cancel context.CancelFunc
	done   map[string]*engine.Span // walked suggestions; a nil span means nothing to show
}

func newSpanState() *spanState { return &spanState{done: map[string]*engine.Span{}} }

// stop cancels the walk in flight, if any.
func (s *spanState) stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel, s.id = nil, ""
}

// reset forgets every result; suggestion IDs belong to one scan.
func (s *spanState) reset() {
	s.stop()
	clear(s.done)
}

// engineMeasure is the real walk. The engine reads only the path of a node
// the stored scan holds for this suggestion, and applies its own time limit.
func engineMeasure(ctx context.Context, d *TuiDeps, op model.Opportunity) (*engine.Span, error) {
	return engine.Measure(ctx, engine.OpportunityOptions{Home: d.Home}, op)
}

// tick turns the spinner while the engine loads or a range is measured.
func (m TuiModel) tick(msg spinner.TickMsg) (tea.Model, tea.Cmd) {
	if !m.Loading && m.span.id == "" && !m.softwareBusy && (m.removing == nil || !m.removing.running) {
		return m, nil
	}
	var cmd tea.Cmd
	m.spin, cmd = m.spin.Update(msg)
	return m, cmd
}

// paneShown reports whether the suggestion detail pane is on screen: beside
// the list on a wide screen, or in place of it after e on a narrow one.
func (m TuiModel) paneShown() bool {
	return m.Tab == TabSuggestions && m.Overlay == OverlayNone && !m.Loading && !m.Intro && (m.Detail || m.w >= 120)
}

// spanTarget is the suggestion whose detail pane is on screen, when a range
// may apply to it. It is read from the engine's own opportunities, never the
// display copy, and no path from anywhere else is used.
func (m TuiModel) spanTarget() (model.Opportunity, bool) {
	var none model.Opportunity
	if m.Deps == nil || m.Deps.Measure == nil || m.Data == nil || m.Data.PlanOps == nil {
		return none, false
	}
	if !m.paneShown() {
		return none, false
	}
	ops := m.Items()
	if m.Cursor < 0 || m.Cursor >= len(ops) {
		return none, false
	}
	for _, op := range m.Data.PlanOps.Opportunities {
		if op.ID == ops[m.Cursor].ID {
			return op, engine.Measurable(op)
		}
	}
	return none, false
}

// syncSpan makes the walk in flight match the detail pane: it cancels a walk
// the pane no longer needs and starts one for a suggestion not walked yet.
func (m TuiModel) syncSpan(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	s := m.span
	op, ok := m.spanTarget()
	if !ok {
		s.stop()
		return m, cmd
	}
	if op.ID == s.id {
		return m, cmd
	}
	s.stop()
	if _, seen := s.done[op.ID]; seen {
		return m, cmd
	}
	s.seq++
	ctx, cancel := context.WithCancel(context.Background())
	s.id, s.cancel = op.ID, cancel
	return m, tea.Batch(cmd, m.walk(ctx, s.seq, op), m.spin.Tick)
}

// walk is the command that measures op.
func (m TuiModel) walk(ctx context.Context, seq int, op model.Opportunity) tea.Cmd {
	deps, s := m.Deps, m.span
	return func() tea.Msg {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return SpanMsg{Seq: seq, ID: op.ID, Err: err}
		}
		sp, err := deps.Measure(ctx, deps, op)
		return SpanMsg{Seq: seq, ID: op.ID, Span: sp, Err: err}
	}
}

// spanned takes a finished walk. A result for any walk but the one in flight
// is dropped. A walk that failed or found nothing is kept as nothing, so it is
// not tried again, and the pane simply shows no range.
func (m TuiModel) spanned(msg SpanMsg) {
	s := m.span
	if msg.Seq != s.seq || msg.ID != s.id {
		return
	}
	s.stop()
	if msg.Err != nil {
		msg.Span = nil
	}
	s.done[msg.ID] = msg.Span
}

// spanLine is the progress line for the pane of suggestion id, or empty.
func (m TuiModel) spanLine(id string) string {
	if m.span.id != id {
		return ""
	}
	return m.spin.View() + " " + m.t.muted.Render("measuring the range…")
}
