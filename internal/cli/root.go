// Package cli implements the macdoc command-line interface. Commands only call
// the engine and render results; they hold no business logic.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"macdoc/internal/model"
)

// version is set at build time via -ldflags.
var version = "dev"

type globalFlags struct {
	json    bool
	noColor bool
	quiet   bool
	verbose bool
}

// NewRootCmd builds the command tree.
func NewRootCmd() *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:           "macdoc",
		Short:         "Explain why your developer Mac is full and slow, and safely get space back",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&g.json, "json", false, "output JSON")
	root.PersistentFlags().BoolVar(&g.noColor, "no-color", os.Getenv("NO_COLOR") != "", "disable colors")
	root.PersistentFlags().BoolVarP(&g.quiet, "quiet", "q", false, "minimal output")
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "verbose output")

	doctor := newDoctorCmd(g)
	root.AddCommand(newVersionCmd(g), doctor, newScanCmd(g),
		newWinsCmd(g), newStorageCmd(g), newDevCmd(g), newExplainCmd(g))
	// Bare `macdoc` shows the doctor summary.
	root.RunE = doctor.RunE
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "macdoc:", err)
		return 1
	}
	return 0
}

func newVersionCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the macdoc version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "version", map[string]string{"version": version})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "macdoc", version)
			return err
		},
	}
}

func writeJSON(w io.Writer, command string, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(model.Envelope{
		SchemaVersion: model.SchemaVersion,
		Command:       command,
		GeneratedAt:   time.Now().UTC(),
		Data:          data,
	})
}
