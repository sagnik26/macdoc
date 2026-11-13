package tests

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sagnik26/macdoc/internal/recipes"
)

// Positive: every kind parses into its kind and identity.
func TestParseMatchAcceptsEveryKind(t *testing.T) {
	for _, c := range []struct{ in, kind, id string }{
		{"bundle:com.docker.docker", "bundle", "com.docker.docker"},
		{"app:Visual Studio Code", "app", "Visual Studio Code"}, // app names may have spaces
		{"brew:ollama", "brew", "ollama"},
		{"npm:yarn", "npm", "yarn"},
	} {
		kind, id, err := recipes.ParseMatch(c.in)
		if err != nil || kind != c.kind || id != c.id {
			t.Errorf("ParseMatch(%q) = %q, %q, %v", c.in, kind, id, err)
		}
	}
}

// Precondition failures: each bad entry is refused with a reason.
func TestParseMatchRefusesBadEntries(t *testing.T) {
	for _, c := range []struct{ in, why string }{
		{"yarn", "kind:identity"},
		{"snap:chromium", "unknown kind"},
		{"npm:", "no identity"},
		{"npm:not a package", "spaces or slashes"},
		{"brew:tap/formula", "spaces or slashes"},
		{"bundle:docker", "has dots"},
	} {
		_, _, err := recipes.ParseMatch(c.in)
		if err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("ParseMatch(%q) err = %v; want one mentioning %q", c.in, err, c.why)
		}
	}
}

// A recipe with a bad match entry fails validation, like any other bad field.
func TestRecipeWithBadMatchFailsValidation(t *testing.T) {
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r := cat.Recipes[0]
	r.Match = []string{"snap:chromium"}
	err = recipes.Validate(r)
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("Validate err = %v; want an unknown-kind problem", err)
	}
}

// loadYAML loads one recipe file through the real loader.
func loadYAML(t *testing.T, body string) error {
	t.Helper()
	fsys := fstest.MapFS{"catalog/x.yaml": &fstest.MapFile{Data: []byte(body)}}
	_, err := recipes.LoadFS(fsys, "catalog")
	return err
}

// Precondition failures for the toolchain. A recipe that names a toolchain
// its software doesn't belong to, that names software with no known
// toolchain and leaves its toolchain out, or whose software spans two
// toolchains, fails to load with a reason.
func TestToolchainDerivationRefusesBadCombinations(t *testing.T) {
	const head = "- id: test.recipe\n  title: t\n  category: package-caches\n  risk: SAFE\n  method: advise\n  detector: path\n"
	for _, c := range []struct{ name, body, why string }{
		{"disagreeing toolchain", head + "  toolchain: docker\n  match: [\"npm:yarn\"]\n", "disagrees"},
		{"no known toolchain", head + "  match: [\"app:Unknown Thing\"]\n", "toolchain is required"},
		{"two toolchains", head + "  match: [\"npm:yarn\", \"tool:pip\"]\n", "several toolchains"},
	} {
		err := loadYAML(t, c.body)
		if err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: err = %v; want one mentioning %q", c.name, err, c.why)
		}
	}
}

// A toolchain left out is filled in from the software it matches.
func TestToolchainIsDerivedFromMatch(t *testing.T) {
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, toolchain string }{
		{"npm.cache", "node"}, {"docker.volumes", "docker"}, {"pip.cache", "python"}, {"xcode.derived_data", "xcode"},
	} {
		r, ok := cat.Get(c.id)
		if !ok || r.Toolchain != c.toolchain {
			t.Errorf("%s toolchain = %q, want %q", c.id, r.Toolchain, c.toolchain)
		}
	}
}
