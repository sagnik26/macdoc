package tests

import (
	"slices"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
)

func privRecipe(risk model.Risk, argv ...string) recipes.Recipe {
	r := r1Native(argv...)
	r.Privileged, r.Risk = true, risk
	return r
}

// Every allow-listed privileged command validates as a privileged recipe.
func TestPrivilegedAllowListValidates(t *testing.T) {
	for _, argv := range recipes.PrivilegedArgvAllowed {
		if err := recipes.Validate(privRecipe(model.RiskCaution, argv...)); err != nil {
			t.Errorf("%q rejected: %v", argv, err)
		}
	}
}

// Anything else is refused: a command off the list, a shell, a flag change,
// a path, a placeholder, a SAFE tier, a dry run, and the same command in an
// ordinary (unprivileged) recipe.
func TestPrivilegedAttacks(t *testing.T) {
	for name, r := range map[string]recipes.Recipe{
		"rm":            privRecipe(model.RiskCaution, "rm", "-rf", "/"),
		"shell":         privRecipe(model.RiskCaution, "sh", "-c", "purge"),
		"extra flag":    privRecipe(model.RiskCaution, "purge", "-f"),
		"other signal":  privRecipe(model.RiskCaution, "killall", "-9", "mDNSResponder"),
		"other process": privRecipe(model.RiskCaution, "killall", "-HUP", "Finder"),
		"path":          privRecipe(model.RiskCaution, "/usr/sbin/purge"),
		"placeholder":   privRecipe(model.RiskCaution, "dscacheutil", recipes.Placeholder),
		"sudo itself":   privRecipe(model.RiskCaution, "sudo", "purge"),
		"safe tier":     privRecipe(model.RiskSafe, "purge"),
	} {
		if err := recipes.Validate(r); err == nil {
			t.Errorf("%s: a bad privileged recipe was accepted", name)
		}
	}
	dry := privRecipe(model.RiskCaution, "purge")
	dry.Native.DryRunArgv = []string{"brew", "autoremove", "-n"}
	if err := recipes.Validate(dry); err == nil {
		t.Error("a privileged recipe with a dry run was accepted")
	}
	// Look-alike: the same commands without the privileged flag are not native commands macdoc runs.
	for _, argv := range recipes.PrivilegedArgvAllowed {
		r := r1Native(argv...)
		if err := recipes.Validate(r); err == nil {
			t.Errorf("%q passed as an ordinary native recipe", argv)
		}
	}
}

// The privileged list is tiny and fixed, and no privileged tool is also an
// ordinary native tool.
func TestPrivilegedListIsPinned(t *testing.T) {
	want := [][]string{{"dscacheutil", "-flushcache"}, {"killall", "-HUP", "mDNSResponder"}, {"purge"}}
	if len(recipes.PrivilegedArgvAllowed) != len(want) {
		t.Fatalf("privileged list = %q; changing it needs an ADR", recipes.PrivilegedArgvAllowed)
	}
	for _, w := range want {
		if !recipes.ArgvIn(w, recipes.PrivilegedArgvAllowed) {
			t.Errorf("%q missing from the privileged list", w)
		}
	}
	for _, a := range recipes.PrivilegedArgvAllowed {
		if slices.Contains(recipes.NativeTools, a[0]) {
			t.Errorf("%s is both a privileged and an ordinary native tool", a[0])
		}
	}
}

// Catalog: every privileged recipe is on request only, and the maintenance
// detector finds nothing when the tool isn't installed.
func TestMaintenanceRecipes(t *testing.T) {
	c, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range c.Recipes {
		if r.Privileged {
			n++
			if !r.OnRequest || !strings.HasPrefix(r.ID, "maintenance.") {
				t.Errorf("%s is privileged but not an on-request maintenance recipe", r.ID)
			}
		}
	}
	if n != 3 {
		t.Errorf("%d privileged recipes, want 3", n)
	}
	old := recipes.ToolInstalled
	recipes.ToolInstalled = func(string) bool { return false }
	defer func() { recipes.ToolInstalled = old }()
	fs, err := r1Run(t, "maintenance.flush_dns", recipes.Env{})
	if err != nil || len(fs) != 0 {
		t.Errorf("tool missing: %+v, %v; want no finding", fs, err)
	}
	recipes.ToolInstalled = func(string) bool { return true }
	fs, err = r1Run(t, "maintenance.flush_dns", recipes.Env{})
	if err != nil || len(fs) != 1 {
		t.Errorf("tool present: %+v, %v; want one finding", fs, err)
	}
}
