package tests

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/engine"
)

func TestDoctorSoftwareText(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "doctor", "software")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Installed software", "Folders no software explains", "Discovery", "Nothing has been changed.")
	if strings.Contains(out, "\x1b[") {
		t.Errorf("plain output should not contain terminal escapes")
	}
}

func TestDoctorSoftwareJSON(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "doctor", "software", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string `json:"command"`
		Data    struct {
			Software     []map[string]any `json:"software"`
			Unattributed []map[string]any `json:"unattributed"`
			New          []string         `json:"new"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "doctor software" {
		t.Errorf("command = %q", env.Command)
	}
	if !strings.Contains(out, `"software": [`) || !strings.Contains(out, `"unattributed": [`) || !strings.Contains(out, `"new": [`) {
		t.Errorf("empty lists should be [] not null: %s", out)
	}
}

func TestDoctorSoftwareNeedsAScan(t *testing.T) {
	empty := t.TempDir()
	cliFixture(t)
	cli.UserHome = func() (string, error) { return empty, nil }
	if _, err := run(t, "doctor", "software"); !errors.Is(err, engine.ErrNoScan) {
		t.Errorf("err = %v, want ErrNoScan", err)
	}
}
