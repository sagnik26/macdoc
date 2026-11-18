package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/config"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
)

// ConfigView is the data behind `macdoc config`.
type ConfigView struct {
	Path     string         `json:"path"`
	Settings map[string]int `json:"settings"` // effective values
	Protect  []string       `json:"protect"`  // yours
	BuiltIn  []string       `json:"built_in_protected"`
	System   []string       `json:"system_protected"`
	// Excluded are recipe IDs you turned off; Notes explain ignored entries.
	Excluded []string `json:"excluded_recipes"`
	Notes    []string `json:"notes,omitempty"`
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
  macdoc config hide <id>               never list this suggestion again (an O-id from the list, or a recipe id like user.trash)
  macdoc config unhide <recipe id>      show it again
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
				BuiltIn: safety.HomeProtected(), System: safety.SystemRoots(), Excluded: append([]string{}, c.ExcludedRecipes...)}
			v.Notes = excludeNotes(c)
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
	hide := &cobra.Command{
		Use:   "hide <id>",
		Short: "Never list this suggestion again (it can only hide, never add)",
		Args:  cobra.ExactArgs(1),
		RunE: edit(func(c *config.Config, args []string) (string, error) {
			id, err := recipeIDFor(args[0])
			if err != nil {
				return "", err
			}
			if err := c.Exclude(id); err != nil {
				return "", err
			}
			return fmt.Sprintf("Hidden: %s. macdoc won't list it. Show it again with macdoc config unhide %s.", id, id), nil
		}),
	}
	unhide := &cobra.Command{
		Use:   "unhide <recipe id>",
		Short: "Undo a hide",
		Args:  cobra.ExactArgs(1),
		RunE: edit(func(c *config.Config, args []string) (string, error) {
			if err := c.Include(args[0]); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s is listed again.", args[0]), nil
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
	cmd.AddCommand(set, protect, unprotect, hide, unhide, path)
	return cmd
}

// recipeIDFor turns what a person typed to hide into a recipe ID: a recipe ID
// (or "prefix.*") that matches a recipe in the catalog, or an O-… ID from the
// latest list, which is looked up to its recipe. Anything else is refused, so
// a typo can't silently hide nothing.
func recipeIDFor(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	cat, err := LoadCatalog()
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.ToUpper(arg), "O-") {
		res, _, err := loadOpportunities(context.Background(), smallBytes)
		if err != nil {
			return "", err
		}
		for _, op := range res.Opportunities {
			if strings.EqualFold(op.ID, arg) {
				return op.RecipeID, nil
			}
		}
		return "", fmt.Errorf("%s isn't in the latest list; use macdoc reclaim --small, or pass a recipe id like user.trash", arg)
	}
	var known []string
	for _, r := range cat.Recipes {
		known = append(known, r.ID)
	}
	if len(config.Config{ExcludedRecipes: config.ExcludeList{arg}}.UnknownExcludes(known)) > 0 {
		return "", fmt.Errorf("%q matches no recipe; use an O-… ID from macdoc reclaim, or a recipe id like user.trash", arg)
	}
	return arg, nil
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
	fmt.Fprintf(&b, "\n%s\n", t.heading("Hidden suggestions (macdoc config unhide <recipe id>)"))
	if len(v.Excluded) == 0 {
		b.WriteString(t.muted.Render(`  None. Hide a suggestion with macdoc config hide <id>, or add "excluded_recipes": ["maintenance.*"] to the settings file.`) + "\n")
	}
	for _, e := range v.Excluded {
		fmt.Fprintf(&b, "  %s\n", t.text.Render(e))
	}
	for _, n := range v.Notes {
		fmt.Fprintf(&b, "  %s\n", t.muted.Render(n))
	}
	fmt.Fprintf(&b, "\n%s\n", t.heading("Always protected"))
	fmt.Fprintf(&b, "  %s\n", t.Wrap(t.muted.Render("~/"+strings.Join(v.BuiltIn, ", ~/")), 2))
	fmt.Fprintf(&b, "  %s\n", t.Wrap(t.muted.Render("and system folders: "+strings.Join(v.System, ", ")), 2))
	fmt.Fprintf(&b, "\n%s %s\n", t.muted.Render("Settings file:"), v.Path)
	_, err := io.WriteString(w, b.String())
	return err
}

// excludeNotes explains excluded entries that match no recipe; they are
// ignored, never an error.
func excludeNotes(c config.Config) []string {
	if len(c.ExcludedRecipes) == 0 {
		return nil
	}
	cat, err := recipes.Load()
	if err != nil {
		return nil
	}
	var known []string
	for _, r := range cat.Recipes {
		known = append(known, r.ID)
	}
	var notes []string
	for _, e := range c.UnknownExcludes(known) {
		notes = append(notes, "ignored: "+e+" matches no known recipe")
	}
	return notes
}
