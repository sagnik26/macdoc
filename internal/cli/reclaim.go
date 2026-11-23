package cli

import (
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/model"
)

// newReclaimCmd builds `macdoc reclaim [toolchain]`: what can be got back,
// ranked by safety. Piped, with --json, --quiet or --dry-run it only lists
// (read-only); at a real terminal it also cleans the SAFE items on Enter
// (ADR 0013).
func newReclaimCmd(g *GlobalFlags) *cobra.Command {
	wins, dev, space := newWinsCmd(g), newDevCmd(g), newStorageCmd(g)
	var categories, toolchains, dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "reclaim [toolchain]",
		Short: "Show what can be cleaned, and clean the risk types you choose (at a terminal)",
		Long: `macdoc reclaim shows what could be removed, by risk, with each suggestion's
size and what it costs to get back.

  macdoc reclaim               every suggestion
  macdoc reclaim --toolchains  space per developer toolchain
  macdoc reclaim xcode         one toolchain (xcode, android, node, homebrew, docker, python, ai, system)
  macdoc reclaim --categories  space by category, "System Data" decoded
  macdoc reclaim --small       also the items that would free under 1 MB

At a terminal it then asks which risk types to clean with a checkbox list:
up and down move, space ticks, a ticks all, Enter starts the cleanup and esc
cancels. SAFE starts ticked. (Without a raw terminal it asks you to type safe,
caution or risky, or all.)
RISKY items are still confirmed one by one, with the plan's code typed.
ADVICE items are shown but can't be removed: macdoc has no action for them.
--tier caution skips the question and cleans only that tier on Enter.
--tier safe|caution --yes cleans that tier with no prompt and no terminal, for scripts:
it only moves items into quarantine (nothing that runs a command or needs a password),
and records "non_interactive" in the audit log. It never cleans RISKY items. Piped,
with --json or --quiet, or with --dry-run, it only lists.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case categories:
				return space.RunE(cmd, nil)
			case toolchains:
				return dev.RunE(cmd, nil)
			}
			if yes && !dryRun && !categories && !toolchains {
				o, err := checkYes(cmd)
				if err != nil {
					return err
				}
				return cleanYes(cmd, g, o, args)
			}
			if canCleanOnEnter(cmd, g, dryRun) {
				o, ok, err := cleanOptions(cmd, args)
				if err != nil {
					return err
				}
				if ok {
					return cleanOnEnter(cmd, g, o)
				}
			}
			if len(args) == 1 {
				return dev.RunE(cmd, args)
			}
			return wins.RunE(cmd, nil)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "with --tier safe or caution: clean that tier with no prompt (quarantine moves only)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only list what would be cleaned, even at a terminal")
	cmd.Flags().BoolVar(&toolchains, "toolchains", false, "space per developer toolchain")
	cmd.Flags().BoolVar(&categories, "categories", false, "space by category, with \"System Data\" decoded")
	cmd.Flags().AddFlagSet(wins.Flags())  // --all, --tier, --small
	cmd.Flags().AddFlagSet(space.Flags()) // --large
	return cmd
}

// cleanOptions turns reclaim's arguments into what to clean. ok is false when
// the request is only a view (a tier that reclaim never cleans), which then
// prints the list as before.
func cleanOptions(cmd *cobra.Command, args []string) (o cleanOpts, ok bool, err error) {
	o.tier = model.RiskSafe
	if t, _ := cmd.Flags().GetString("tier"); t != "" {
		r, err := ParseTier(t)
		if err != nil {
			return o, false, err
		}
		if r != model.RiskSafe && r != model.RiskCaution {
			return o, false, nil // RISKY and advice-only items are never cleaned here
		}
		o.tier, o.tierSet = r, true
	}
	o.all, _ = cmd.Flags().GetBool("all")
	if len(args) == 1 {
		if o.toolchain, err = parseToolchain(args[0]); err != nil {
			return o, false, err
		}
	}
	return o, true, nil
}
