package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// newVitalsCmd builds `macdoc vitals`: how the Mac is doing right now. By
// default the live reading with the health score; --memory asks whether
// memory is what slows it down. Read-only: it never quits an app.
func newVitalsCmd(g *GlobalFlags) *cobra.Command {
	status, slow := newStatusCmd(g), newSlowCmd(g)
	var memory bool
	cmd := &cobra.Command{
		Use:   "vitals",
		Short: "How your Mac is doing now: CPU, memory, disk, network, battery and a health score (read-only)",
		Long: `macdoc vitals reads the Mac's live state and scores it from 0 to 100, with the
reasons it is lower. Zombie and busy processes are listed; none is ever ended.

  macdoc vitals                  the live reading
  macdoc vitals --watch          a new reading every --interval (NDJSON with --json)
  macdoc vitals --memory         is memory what is slowing it down? (swap history, biggest users)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if memory {
				if watch, _ := cmd.Flags().GetBool("watch"); watch {
					return errors.New("--watch isn't available with --memory")
				}
				return slow.RunE(cmd, nil)
			}
			return status.RunE(cmd, nil)
		},
	}
	cmd.Flags().BoolVar(&memory, "memory", false, "is memory slowing it down? swap history and the biggest memory users")
	cmd.Flags().AddFlagSet(status.Flags()) // --watch, --interval
	cmd.Flags().AddFlagSet(slow.Flags())   // --days, --tips
	return cmd
}
