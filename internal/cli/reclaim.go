package cli

import "github.com/spf13/cobra"

// newReclaimCmd builds `macdoc reclaim [toolchain]`: what can be got back,
// ranked by safety, with the IDs `macdoc apply` takes. Read-only.
func newReclaimCmd(g *GlobalFlags) *cobra.Command {
	wins, dev, space := newWinsCmd(g), newDevCmd(g), newStorageCmd(g)
	var categories, toolchains bool
	cmd := &cobra.Command{
		Use:   "reclaim [toolchain]",
		Short: "What you can get back, ranked by safety, with IDs to apply (read-only)",
		Long: `macdoc reclaim lists what could be removed, by risk, with each suggestion's
ID, size and what it costs to get back. Nothing changes.

  macdoc reclaim               every suggestion
  macdoc reclaim --toolchains  space per developer toolchain
  macdoc reclaim xcode         one toolchain (xcode, android, node, homebrew, docker, python, ai, system)
  macdoc reclaim --categories  space by category, "System Data" decoded

Then: macdoc apply <ids> (or --safe) to remove them after you confirm.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case categories:
				return space.RunE(cmd, nil)
			case toolchains:
				return dev.RunE(cmd, nil)
			case len(args) == 1:
				return dev.RunE(cmd, args)
			}
			return wins.RunE(cmd, nil)
		},
	}
	cmd.Flags().BoolVar(&toolchains, "toolchains", false, "space per developer toolchain")
	cmd.Flags().BoolVar(&categories, "categories", false, "space by category, with \"System Data\" decoded")
	cmd.Flags().AddFlagSet(wins.Flags())  // --all, --tier
	cmd.Flags().AddFlagSet(space.Flags()) // --large
	return cmd
}
