package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// toolchainAliases maps short names users are likely to type.
var toolchainAliases = map[string]string{"brew": "homebrew"}

// DevResult is the data behind `macdoc reclaim`.
type DevResult struct {
	Scan       model.ScanInfo          `json:"scan"`
	ScanAge    string                  `json:"scan_age"`
	Toolchain  string                  `json:"toolchain,omitempty"` // the requested one, if any
	Toolchains []engine.ToolchainTotal `json:"toolchains"`
	Warnings   []string                `json:"warnings,omitempty"`
}

func newDevCmd(g *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "dev [toolchain]",
		Short: "Reclaimable space per developer toolchain (read-only)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tc := ""
			if len(args) == 1 {
				var err error
				if tc, err = parseToolchain(args[0]); err != nil {
					return err
				}
			}
			res, cat, err := loadOpportunities(context.Background())
			if err != nil {
				return err
			}
			dev := BuildDevResult(res, tc)
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "reclaim toolchain", dev, dev.Warnings)
			}
			return RenderDev(cmd.OutOrStdout(), dev, cat)
		},
	}
}

func parseToolchain(s string) (string, error) {
	s = strings.ToLower(s)
	if a, ok := toolchainAliases[s]; ok {
		s = a
	}
	if !slices.Contains(recipes.Toolchains, s) {
		return "", fmt.Errorf("unknown toolchain %q; valid: %s", s, strings.Join(recipes.Toolchains, ", "))
	}
	return s, nil
}

// BuildDevResult selects the suggestions of one toolchain (or all of them
// when tc is empty) and totals them.
func BuildDevResult(res engine.OpportunityResult, tc string) DevResult {
	groups := engine.ByToolchain(res.Opportunities)
	if tc != "" {
		var only []engine.ToolchainTotal
		for _, gr := range groups {
			if gr.Toolchain == tc {
				only = append(only, gr)
			}
		}
		groups = only
	}
	if groups == nil {
		groups = []engine.ToolchainTotal{}
	}
	return DevResult{Scan: res.Scan, ScanAge: res.ScanAge, Toolchain: tc, Toolchains: groups, Warnings: res.Warnings}
}

// RenderDev writes the suggestions of one toolchain in the human format.
func RenderDev(w io.Writer, d DevResult, cat *recipes.Catalog) error {
	d = display(d) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	b.WriteString(t.commandHeader("reclaim", d.Scan.ID, d.ScanAge))
	if len(d.Toolchains) == 0 {
		if d.Toolchain != "" {
			fmt.Fprintf(&b, " Nothing for %s in the latest scan.\n\n", d.Toolchain)
		} else {
			b.WriteString(" Nothing to reclaim was found in the latest scan.\n\n")
		}
	}
	for _, gr := range d.Toolchains {
		b.WriteString(t.section(t.accent, gr.Toolchain, tierTotal(gr.Bytes, gr.Opportunities)))
		writeOpportunities(&b, t, gr.Opportunities, cat, d.Toolchain != "")
		b.WriteString("\n")
	}
	writeNotes(&b, t, d.Warnings)
	b.WriteString(t.footer("macdoc apply <id…> (shows the plan, then asks you to confirm)"))
	_, err := io.WriteString(w, b.String())
	return err
}
