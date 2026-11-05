package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"macdoc/internal/config"
	"macdoc/internal/safety"
)

// ConfigView is the data behind `macdoc config`.
type ConfigView struct {
	Path     string         `json:"path"`
	Settings map[string]int `json:"settings"` // effective values
	Protect  []string       `json:"protect"`  // yours
	BuiltIn  []string       `json:"built_in_protected"`
	System   []string       `json:"system_protected"`
}

func newConfigCmd(g *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or change macdoc's settings and your protected folders",
		Long: `macdoc config shows the settings. Settings can only make macdoc stricter
(more protected folders) or change timings within fixed limits.

  macdoc config                         show everything
  macdoc config set <setting> <number>  change a setting (0 restores the default)
  macdoc config protect <folder>        never touch this folder or anything in it
  macdoc config unprotect <folder>      undo a protect (built-in ones stay)
  macdoc config path                    where the settings file is`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			c, err := config.Load(home)
			if err != nil {
				return err
			}
			v := ConfigView{Path: config.Path(home), Settings: map[string]int{}, Protect: append([]string{}, c.Protect...),
				BuiltIn: safety.HomeProtected(), System: safety.SystemRoots()}
			for _, s := range config.Settings {
				v.Settings[s.Key] = c.Effective(s.Key)
			}
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "config", v)
			}
			return renderConfig(cmd.OutOrStdout(), c, v)
		},
	}

	edit := func(fn func(c *config.Config, args []string) (string, error)) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			c, err := config.Load(home)
			if err != nil {
				return err
			}
			msg, err := fn(&c, args)
			if err != nil {
				return err
			}
			if err := config.Save(home, c); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), msg)
			return err
		}
	}
	set := &cobra.Command{
		Use:   "set <setting> <number>",
		Short: "Change a setting (0 restores the default)",
		Args:  cobra.ExactArgs(2),
		RunE: edit(func(c *config.Config, args []string) (string, error) {
			n, err := strconv.Atoi(args[1])
			if err != nil {
				return "", fmt.Errorf("%q isn't a whole number", args[1])
			}
			if err := c.Set(args[0], n); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s is now %d.", args[0], c.Effective(args[0])), nil
		}),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			var keys []string
			for _, s := range config.Settings {
				keys = append(keys, s.Key+"\t"+s.Help)
			}
			return keys, cobra.ShellCompDirectiveNoFileComp
		},
	}
	protect := &cobra.Command{
		Use:   "protect <folder>",
		Short: "Never touch this folder or anything in it",
		Args:  cobra.ExactArgs(1),
		RunE: edit(func(c *config.Config, args []string) (string, error) {
			if err := c.AddProtect(args[0]); err != nil {
				return "", err
			}
			return fmt.Sprintf("Protected %s. macdoc won't suggest or remove anything in it.", args[0]), nil
		}),
	}
	unprotect := &cobra.Command{
		Use:   "unprotect <folder>",
		Short: "Undo a protect (built-in protected folders stay protected)",
		Args:  cobra.ExactArgs(1),
		RunE: edit(func(c *config.Config, args []string) (string, error) {
			if err := c.RemoveProtect(args[0]); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s is no longer in your protected list.", args[0]), nil
		}),
	}
	path := &cobra.Command{
		Use:   "path",
		Short: "Print where the settings file is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), config.Path(home))
			return err
		},
	}
	cmd.AddCommand(set, protect, unprotect, path)
	return cmd
}

func renderConfig(w io.Writer, c config.Config, v ConfigView) error {
	c, v = display(c), display(v) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	fmt.Fprintf(&b, "%s\n", t.heading("Settings"))
	for _, s := range config.Settings {
		val := strconv.Itoa(c.Effective(s.Key))
		note := fmt.Sprintf("default %d, %d–%d", s.Default, s.Min, s.Max)
		if c.Get(s.Key) == 0 {
			note = fmt.Sprintf("default, %d–%d", s.Min, s.Max)
		}
		fmt.Fprintf(&b, "  %s %s  %s\n", t.accent.Render(fmt.Sprintf("%-24s", s.Key)), t.size.Render(fmt.Sprintf("%4s", val)),
			t.muted.Render(s.Help+" ("+note+")"))
	}

	fmt.Fprintf(&b, "\n%s\n", t.heading("Your protected folders"))
	if len(v.Protect) == 0 {
		b.WriteString(t.muted.Render("  None yet. Add one with macdoc config protect ~/path/to/folder") + "\n")
	}
	for _, p := range v.Protect {
		fmt.Fprintf(&b, "  %s\n", t.text.Render(p))
	}
	fmt.Fprintf(&b, "\n%s\n", t.heading("Always protected"))
	fmt.Fprintf(&b, "  %s\n", t.Wrap(t.muted.Render("~/"+strings.Join(v.BuiltIn, ", ~/")), 2))
	fmt.Fprintf(&b, "  %s\n", t.Wrap(t.muted.Render("and system folders: "+strings.Join(v.System, ", ")), 2))
	fmt.Fprintf(&b, "\n%s %s\n", t.muted.Render("Settings file:"), v.Path)
	_, err := io.WriteString(w, b.String())
	return err
}
