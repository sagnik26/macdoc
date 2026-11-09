package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
)

// ExplainResult is the data behind `macdoc explain`.
type ExplainResult struct {
	Scan        model.ScanInfo    `json:"scan"`
	Opportunity model.Opportunity `json:"opportunity"`
	BaseRisk    model.Risk        `json:"base_risk"` // the recipe's risk before context raised it
	// BytesLower and BytesUpper are the least and the most that removing the
	// item frees, measured when the explanation was made. They are set only
	// when the item's files looked shared with copies (ADR 0005, stage 3).
	// BytesLower alone means the most could not be worked out; neither means
	// the size above is the whole answer. Unknown is absent, never zero.
	BytesLower *int64 `json:"bytes_lower,omitempty"`
	BytesUpper *int64 `json:"bytes_upper,omitempty"`
}

func newExplainCmd(g *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "explain <O-id>",
		Short: "What an item is, why it's there, what it costs to restore and the exact action (read-only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, cat, err := loadOpportunities(context.Background())
			if err != nil {
				return err
			}
			op, ok := engine.FindOpportunity(res, args[0])
			if !ok {
				return fmt.Errorf(`%s not found in the latest scan; IDs change after a new scan`, args[0])
			}
			ex := ExplainResult{Scan: res.Scan, Opportunity: op, BaseRisk: op.Risk}
			if base, raised := engine.RaisedFrom(op, cat); raised {
				ex.BaseRisk = base
			}
			if warn := withSpan(cmd.Context(), &ex); warn != "" {
				res.Warnings = append(res.Warnings, warn)
			}
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "explain", ex, res.Warnings)
			}
			return RenderExplain(cmd.OutOrStdout(), ex)
		},
	}
}

// withSpan adds the measured range to ex when the item's files look shared. A
// walk that fails or runs out of time costs the range, never the explanation;
// the returned text says so, or is empty.
func withSpan(ctx context.Context, ex *ExplainResult) string {
	home, err := UserHome()
	if err != nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sp, err := engine.Measure(ctx, engine.OpportunityOptions{Home: home}, ex.Opportunity)
	if err != nil {
		return "could not measure how much of this is shared with copies: " + err.Error()
	}
	if sp != nil {
		ex.BytesLower, ex.BytesUpper = &sp.Lower, sp.Upper
	}
	return ""
}

// setSpan records a measured range; a nil sp leaves the explanation as it was.
func (ex *ExplainResult) setSpan(sp *engine.Span) {
	if sp != nil {
		ex.BytesLower, ex.BytesUpper = &sp.Lower, sp.Upper
	}
}

// RenderExplain writes one suggestion in detail in the human format.
func RenderExplain(w io.Writer, ex ExplainResult) error {
	return renderExplain(w, ex, "")
}

// renderExplain is RenderExplain with an optional progress line, already
// styled, shown under the size note while the range is being measured.
func renderExplain(w io.Writer, ex ExplainResult, busy string) error {
	ex = display(ex) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	op := ex.Opportunity
	row := func(label, value string) {
		fmt.Fprintf(&b, "  %s %s\n", t.muted.Render(fmt.Sprintf("%-11s", label)), t.Wrap(value, 14))
	}

	if t.on {
		b.WriteString(t.Card(t.Badge(op.Risk) + " " + t.ID.Render(op.ID) + t.faint.Render(" · ") + t.Title.Render(op.Title)))
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "%s · %s\n\n", op.ID, op.Title)
	}
	if op.Target != "" {
		row("Target", t.text.Render(op.Target))
	}
	row("Size", t.size.Render(sizeWithSource(op.Bytes)))
	for _, line := range sizeLines(t, ex, busy) {
		row("", line)
	}
	risk := t.riskStyle(op.Risk).Render(string(op.Risk))
	if ex.BaseRisk != "" && ex.BaseRisk != op.Risk {
		reason := op.RiskReason
		if reason == "" {
			reason = "it changed in the last 7 days"
		}
		risk += t.muted.Render(fmt.Sprintf(" (raised from %s: %s)", ex.BaseRisk, reason))
	}
	row("Risk", risk)
	row("Idle", idleSince(op.IdleDays))
	switch op.Method {
	case model.MethodNative:
		row("Method", "the tool's own command: "+t.Command(op.Command))
		if len(op.DryRun) > 0 {
			row("", t.muted.Render("preview with: ")+t.Command(op.DryRun))
		}
	case model.MethodQuarantine:
		row("Method", "deleted by macdoc after you confirm (checked again first)")
	default:
		row("Method", "advice only; macdoc takes no action")
	}
	row("Restore", restoreDetail(op.Restore))
	row("Category", op.Category+" · "+op.Toolchain)
	row("Confidence", fmt.Sprintf("%.0f%%", op.Confidence*100))

	if op.Explain != "" {
		fmt.Fprintf(&b, "\n%s\n  %s\n", t.heading("What it is"), t.Wrap(t.text.Render(op.Explain), 2))
	}
	if len(op.Steps) > 0 {
		heading := "How to free it yourself"
		if op.Method != model.MethodAdvise {
			heading = "To do it by hand instead"
		}
		b.WriteString("\n" + t.heading(heading) + "\n")
		for i, s := range op.Steps {
			fmt.Fprintf(&b, "  %s %s\n", t.accent.Render(fmt.Sprintf("%d.", i+1)), t.Wrap(stepText(t, s), 5))
		}
	}
	if op.Note != "" {
		fmt.Fprintf(&b, "\n%s\n  %s\n", t.heading("Note"), t.Wrap(t.warn.Render(op.Note), 2))
	}
	b.WriteString("\n" + t.done("Nothing has been changed.") + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// idleSince says how long ago the item last changed; a negative count is unknown.
func idleSince(days int) string {
	if days < 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d days since last change", days)
}

// sizeLines are the lines under the size of a measured suggestion: the note
// on what removing it frees, and the progress line while the range is being
// measured. Other sizes have none.
func sizeLines(t *Theme, ex ExplainResult, busy string) []string {
	if ex.Opportunity.Bytes.Source != model.SourceMeasured {
		return nil
	}
	lines := []string{t.muted.Render(sizeNote(ex))}
	if busy != "" {
		lines = append(lines, busy)
	}
	return lines
}

// stepText renders a step with each `command` styled as one to copy. The
// backticks are dropped, so the plain theme prints just the text.
func stepText(t *Theme, s string) string {
	parts := strings.Split(s, "`")
	for i, p := range parts {
		if i%2 == 1 {
			parts[i] = t.accent.Render(p)
		} else {
			parts[i] = t.text.Render(p)
		}
	}
	return strings.Join(parts, "")
}

// SizeNote explains a measured suggestion size: the space removing the item
// is certain to free, which can be less than the folder's total when files in
// it share space with copies (APFS clones).
const SizeNote = "This is the space removing it is certain to free. It can be less than the folder's total when its files share space with copies."

// sizeNote explains the size: the range when one was measured and the two
// ends differ, else the plain note. With no upper end it gives the lower end
// only, so an unknown is never shown as a number.
func sizeNote(ex ExplainResult) string {
	lo, hi := ex.BytesLower, ex.BytesUpper
	switch {
	case lo == nil:
		return SizeNote
	case hi == nil:
		return "Removing this frees at least " + engine.DecimalBytes(*lo) + ". The most it could free could not be worked out."
	case *hi <= *lo:
		return SizeNote
	}
	return "Removing this frees at least " + engine.DecimalBytes(*lo) + " and at most " + engine.DecimalBytes(*hi) +
		". The difference is space shared with copies of its files."
}

// sizeWithSource shows a size and where it came from; unknown is never 0.
func sizeWithSource(v model.Bytes) string {
	if v.Source == model.SourceUnknown {
		s := "—  unknown"
		if v.Reason != "" {
			s += " (" + v.Reason + ")"
		}
		return s
	}
	s := fmt.Sprintf("%s (%s)", engine.DecimalBytes(v.Value), v.Source)
	if v.Reason != "" {
		s += ": " + v.Reason
	}
	return s
}

func restoreDetail(r model.Restore) string {
	var s string
	switch r.Kind {
	case model.RestoreRebuild:
		s = "rebuilds on next build"
	case model.RestoreRedownload:
		s = "re-downloads on demand"
		if r.RedownloadBytes > 0 {
			s = "re-download ~" + engine.DecimalBytes(r.RedownloadBytes)
		}
	case model.RestoreIrreplaceable:
		s = "can't be restored"
	default:
		s = "nothing to restore"
	}
	if r.Note != "" {
		s += ". " + r.Note
	}
	return s
}
