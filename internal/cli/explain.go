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
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "explain", ex, res.Warnings)
			}
			return RenderExplain(cmd.OutOrStdout(), ex)
		},
	}
}

func RenderExplain(w io.Writer, ex ExplainResult) error {
	ex = display(ex) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	op := ex.Opportunity
	row := func(label, value string) {
		fmt.Fprintf(&b, "  %s %s\n", t.muted.Render(fmt.Sprintf("%-11s", label)), t.Wrap(value, 14))
	}

	if t.on {
		b.WriteString(t.Card(t.Badge(op.Risk) + " " + t.Id.Render(op.ID) + t.faint.Render(" · ") + t.Title.Render(op.Title)))
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "%s · %s\n\n", op.ID, op.Title)
	}
	if op.Target != "" {
		row("Target", t.text.Render(op.Target))
	}
	row("Size", t.size.Render(sizeWithSource(op.Bytes)))
	risk := t.riskStyle(op.Risk).Render(string(op.Risk))
	if ex.BaseRisk != "" && ex.BaseRisk != op.Risk {
		reason := op.RiskReason
		if reason == "" {
			reason = "it changed in the last 7 days"
		}
		risk += t.muted.Render(fmt.Sprintf(" (raised from %s: %s)", ex.BaseRisk, reason))
	}
	row("Risk", risk)
	if op.IdleDays >= 0 {
		row("Idle", fmt.Sprintf("%d days since last change", op.IdleDays))
	} else {
		row("Idle", "unknown")
	}
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

// stepText renders a step with each `command` styled as one to copy. The
// backticks are dropped, so the plain theme prints just the text.
func stepText(t *theme, s string) string {
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
