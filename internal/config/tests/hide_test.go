package tests

import (
	"testing"

	"github.com/sagnik26/macdoc/internal/config"
)

// Hiding adds a recipe ID (or family) once, refuses anything that isn't an ID,
// and unhiding removes it. It can only subtract: nothing here adds a recipe.
func TestExcludeAndInclude(t *testing.T) {
	home := t.TempDir()
	c, _ := config.Load(home)
	for _, bad := range []string{"", " ", "Bad ID", "*", "a..b", "../x", "user.trash/../x"} {
		if err := c.Exclude(bad); err == nil {
			t.Errorf("Exclude(%q) was accepted", bad)
		}
	}
	if err := c.Exclude("user.trash"); err != nil {
		t.Fatal(err)
	}
	if err := c.Exclude("user.trash"); err != nil || len(c.ExcludedRecipes) != 1 {
		t.Fatalf("twice: %v %v", err, c.ExcludedRecipes)
	}
	if !c.Excludes("user.trash") || c.Excludes("user.trash2") {
		t.Error("Excludes after Exclude")
	}
	if err := config.Save(home, c); err != nil {
		t.Fatal(err)
	}
	c, _ = config.Load(home)
	if !c.Excludes("user.trash") {
		t.Error("not saved")
	}
	if err := c.Include("user.nothing"); err == nil {
		t.Error("Include of an ID that isn't hidden should fail")
	}
	if err := c.Include("user.trash"); err != nil || c.Excludes("user.trash") {
		t.Errorf("Include: %v %v", err, c.ExcludedRecipes)
	}
}
