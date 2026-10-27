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

func newStorageCmd(g *globalFlags) *cobra.Command {
	var large bool
	cmd := &cobra.Command{
		Use:   "storage",
		Short: `Where space goes, by category, with "System Data" decoded (read-only)`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := userHome()
			if err != nil {
				return err
			}
			cat, err := loadCatalog()
			if err != nil {
				return err
			}
			res, err := engine.Storage(context.Background(), engine.StorageOptions{
				OpportunityOptions: engine.OpportunityOptions{Home: home, Runner: runner, Catalog: cat},
				Large:              large,
			})
			if err != nil {
				return err
			}
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "storage", res)
			}
			return renderStorage(cmd.OutOrStdout(), res, large)
		},
	}
	cmd.Flags().BoolVar(&large, "large", false, "also list big items no recipe covers (advice only)")
	return cmd
}

func renderStorage(w io.Writer, res engine.StorageResult, large bool) error {
	var b strings.Builder
	fmt.Fprintf(&b, "macdoc storage · scan %s · %s\n\n", res.Scan.ID, res.ScanAge)

	b.WriteString(" Reclaimable, by category\n")
	if len(res.Categories) == 0 {
		b.WriteString("  Nothing to reclaim was found in the latest scan.\n")
	}
	for _, c := range res.Categories {
		size := engine.DecimalBytes(c.Bytes)
		if c.UnknownCount > 0 {
			size += "+"
		}
		fmt.Fprintf(&b, "  %-22s %9s  %3d item(s)  %s\n", c.Category, size, c.Count, byRisk(c))
	}

	b.WriteString("\n \"System Data\", decoded\n")
	for _, p := range res.SystemData {
		fmt.Fprintf(&b, "  %-58s %s\n", truncate(p.Name, 58), partSize(p.Bytes))
		fmt.Fprintf(&b, "      %s\n", p.Explain)
	}

	if large {
		b.WriteString("\n Large items with no recipe (≥ 1 GB): advice only, review manually\n")
		if len(res.Large) == 0 {
			b.WriteString("  None.\n")
		}
		for _, n := range res.Large {
			fmt.Fprintf(&b, "  %9s  %s\n", engine.DecimalBytes(n.Bytes), n.Path)
		}
	}
	b.WriteString("\n")
	writeNotes(&b, res.Warnings)
	b.WriteString(" macdoc wins · macdoc dev · nothing has been changed\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// byRisk lists a category's known bytes per tier, in tier order.
func byRisk(c engine.CategoryTotal) string {
	var parts []string
	for _, r := range engine.TierOrder {
		if v, ok := c.ByRisk[r]; ok {
			parts = append(parts, fmt.Sprintf("%s %s", r, engine.DecimalBytes(v)))
		}
	}
	return strings.Join(parts, " · ")
}

// partSize renders a System Data size; unknown says why instead of "0".
func partSize(v model.Bytes) string {
	switch v.Source {
	case model.SourceUnknown:
		if v.Reason != "" {
			return "—  unknown (" + v.Reason + ")"
		}
		return "—  unknown"
	case model.SourceEstimated:
		return "~" + engine.DecimalBytes(v.Value) + " (estimated)"
	}
	return fmt.Sprintf("%s (%s)", engine.DecimalBytes(v.Value), v.Source)
}
