package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/recipes"
)

// Items that would free under 1 MB are counted in a note by default, and
// listed with --small.
func TestReclaimSmallListsTinyItems(t *testing.T) {
	home := cliFixture(t)
	cliMu.Lock()
	cliFindings["npm.cache"] = append(cliFindings["npm.cache"], testTinyFinding(home))
	cliMu.Unlock()

	out, err := run(t, "reclaim")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "1 item(s) that would free under 1 MB not shown", "macdoc reclaim --small")
	if strings.Contains(out, "Tiny cache") {
		t.Errorf("the tiny item is listed without --small:\n%s", out)
	}

	out, err = run(t, "reclaim", "--small")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Tiny cache", "200 KB")
	if strings.Contains(out, "under 1 MB not shown") {
		t.Errorf("--small still counts it as hidden:\n%s", out)
	}
}

func testTinyFinding(home string) (f recipes.Finding) {
	return recipes.Finding{Target: filepath.Join(home, ".npm/_tiny"), Bytes: measured(200_000), Title: "Tiny cache"}
}
