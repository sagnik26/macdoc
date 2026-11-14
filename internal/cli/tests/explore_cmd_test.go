package tests

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/engine"
)

// The command prints the folder, says nothing was changed, and for a folder
// the scan kept no detail on it says so instead of inventing a size.
func TestDoctorExploreText(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "explore", "/definitely/not/in/the/scan")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "explore", "unavailable", "the scan kept no detail", "Nothing has been changed.")
}

// --json has the documented fields, with empty lists as [] and no size for a
// folder the scan didn't measure.
func TestDoctorExploreJSON(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "explore", "/definitely/not/in/the/scan", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string               `json:"command"`
		Data    engine.ExploreResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "explore" || env.Data.Status != "unavailable" || env.Data.Bytes != nil {
		t.Errorf("envelope = %+v", env)
	}
	if !strings.Contains(out, `"entries": [`) || !strings.Contains(out, `"largest_files": [`) {
		t.Errorf("empty lists should be [] not null: %s", out)
	}
}

// A relative path is refused: the path is only for looking, and must be clear.
func TestDoctorExploreRefusesARelativePath(t *testing.T) {
	cliFixture(t)
	if _, err := run(t, "explore", "relative/dir"); err == nil {
		t.Error("a relative path should be refused")
	}
}

func TestDoctorExploreNeedsAScan(t *testing.T) {
	empty := t.TempDir()
	cliFixture(t)
	cli.UserHome = func() (string, error) { return empty, nil }
	if _, err := run(t, "explore"); !errors.Is(err, engine.ErrNoScan) {
		t.Errorf("err = %v, want ErrNoScan", err)
	}
}
