package tests

import (
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/recipes"
)

// Positive: a folder and an extension come out of a valid entry.
func TestSplitKeepFileAcceptsValidEntries(t *testing.T) {
	for _, c := range []struct{ in, dir, ext string }{
		{"~/Downloads/*.dmg", "~/Downloads", ".dmg"},
		{"~/Library/Caches/Homebrew/downloads/*.pkg", "~/Library/Caches/Homebrew/downloads", ".pkg"},
		{"~/Downloads/*.DMG", "~/Downloads", ".DMG"},
	} {
		dir, ext, ok := recipes.SplitKeepFile(c.in)
		if !ok || dir != c.dir || ext != c.ext {
			t.Errorf("SplitKeepFile(%q) = %q, %q, %v", c.in, dir, ext, ok)
		}
	}
}

// Precondition failures: entries that could match too much, or something
// other than a folder and an extension, are refused.
func TestSplitKeepFileRefusesBadEntries(t *testing.T) {
	for _, in := range []string{
		"~/Downloads/*",             // no extension
		"~/Downloads/*.",            // empty extension
		"~/Downloads/a.dmg",         // a name, not a pattern
		"/Users/me/Downloads/*.dmg", // not ~-relative
		"~/*/Downloads/*.dmg",       // glob in the folder
		"~/../etc/*.dmg",            // climbs out of home
		"~/*.dmg",                   // the folder is home itself with no name
		"~/Downloads/*.d/g",         // path separator in the extension
		"~/Downloads/*.tar.gz;rm",   // junk in the extension
	} {
		if _, _, ok := recipes.SplitKeepFile(in); ok {
			t.Errorf("SplitKeepFile(%q) was accepted", in)
		}
	}
}

// The callback built from the real catalog keeps installers in Downloads,
// whatever their case, and nothing else.
func TestKeepFileFuncFromTheCatalog(t *testing.T) {
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	keep := recipes.KeepFileFunc(cat, "/Users/me")
	if keep == nil {
		t.Fatal("the catalog should name files to keep")
	}
	for _, c := range []struct {
		dir, name string
		want      bool
	}{
		{"/Users/me/Downloads", "app.dmg", true},
		{"/Users/me/Downloads", "APP.DMG", true},
		{"/Users/me/Downloads", "setup.pkg", true},
		{"/Users/me/downloads", "app.dmg", true},      // folder case-insensitive
		{"/Users/me/Downloads", "notes.txt", false},   // not an installer
		{"/Users/me/Downloads", "app.dmg.txt", false}, // look-alike
		{"/Users/me/Downloads", "dmg", false},         // no extension
		{"/Users/me/Downloads/sub", "app.dmg", false}, // not directly in the folder
		{"/Users/me/Documents", "app.dmg", false},     // another folder
		{"/Users/other/Downloads", "app.dmg", false},  // another home
	} {
		if got := keep(c.dir, c.name); got != c.want {
			t.Errorf("keep(%q, %q) = %v, want %v", c.dir, c.name, got, c.want)
		}
	}
}

// A recipe with a bad keep_files entry fails to load, with the entry named.
func TestLoaderRefusesABadKeepFilesEntry(t *testing.T) {
	const head = "- id: test.recipe\n  title: t\n  category: package-caches\n  toolchain: system\n  risk: SAFE\n  method: advise\n  detector: path\n"
	err := loadYAML(t, head+"  keep_files: [\"~/Downloads/*\"]\n")
	if err == nil || !strings.Contains(err.Error(), "keep_files") {
		t.Errorf("err = %v; want one naming keep_files", err)
	}
}
