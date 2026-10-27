package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/model"
)

// ExplainResult is the data behind `macdoc explain`.
type ExplainResult struct {
	Scan        model.ScanInfo    `json:"scan"`
	Opportunity model.Opportunity `json:"opportunity"`
	BaseRisk    model.Risk        `json:"base_risk"` // the recipe's risk before context raised it
}

func newExplainCmd(g *globalFlags) *cobra.Command {
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
			return renderExplain(cmd.OutOrStdout(), ex)
		},
	}
}

func renderExplain(w io.Writer, ex ExplainResult) error {
	var b strings.Builder
	op := ex.Opportunity
	row := func(label, value string) { fmt.Fprintf(&b, "  %-11s %s\n", label, value) }

	fmt.Fprintf(&b, "%s · %s\n\n", op.ID, op.Title)
	if op.Target != "" {
		row("Target", op.Target)
	}
	row("Size", sizeWithSource(op.Bytes))
	risk := string(op.Risk)
	if ex.BaseRisk != "" && ex.BaseRisk != op.Risk {
		risk += fmt.Sprintf(" (raised from %s: it changed in the last 7 days)", ex.BaseRisk)
	}
	row("Risk", risk)
	if op.IdleDays >= 0 {
		row("Idle", fmt.Sprintf("%d days since last change", op.IdleDays))
	} else {
		row("Idle", "unknown")
	}
	switch op.Method {
	case model.MethodNative:
		row("Method", "the tool's own command: "+strings.Join(op.Command, " "))
		if len(op.DryRun) > 0 {
			row("", "preview with: "+strings.Join(op.DryRun, " "))
		}
	case model.MethodQuarantine:
		row("Method", "moved to macdoc quarantine; restorable for 7 days")
	default:
		row("Method", "advice only; macdoc takes no action")
	}
	row("Restore", restoreDetail(op.Restore))
	row("Category", op.Category+" · "+op.Toolchain)
	row("Confidence", fmt.Sprintf("%.0f%%", op.Confidence*100))

	if op.Explain != "" {
		fmt.Fprintf(&b, "\nWhat it is\n  %s\n", op.Explain)
	}
	if op.Note != "" {
		fmt.Fprintf(&b, "\nNote\n  %s\n", op.Note)
	}
	b.WriteString("\nNothing has been changed.\n")
	_, err := io.WriteString(w, b.String())
	return err
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
