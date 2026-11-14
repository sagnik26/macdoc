package tests

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"

	"github.com/sagnik26/macdoc/internal/config"
)

func TestConfigCommand(t *testing.T) {
	home := cliFixture(t)
	out, err := run(t, "config")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "plan_minutes               30", "(default, 5–120)", "None yet.", "~/Documents", "/System",
		"Settings file: "+filepath.Join(home, "Library/Application Support/macdoc/config.json"))

	for _, args := range [][]string{
		{"config", "set", "plan_minutes", "45"},
		{"config", "protect", "~/code/a"},
	} {
		if _, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	out, _ = run(t, "config")
	mustContain(t, out, "plan_minutes               45", "(default 30, 5–120)", "  ~/code/a\n")

	// A protected folder drops its suggestions: ~/code/a/node_modules is gone.
	out, err = run(t, "reclaim", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Stale node_modules folder · a ") || !strings.Contains(out, "Stale node_modules folder · b ") || !strings.Contains(out, "(protected)") {
		t.Errorf("protect didn't drop the suggestion:\n%s", out)
	}

	out, err = run(t, "config", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data cli.ConfigView `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Data.Settings["plan_minutes"] != 45 || len(env.Data.Protect) != 1 {
		t.Errorf("json: %+v, %v", env.Data, err)
	}

	for _, bad := range [][]string{
		{"config", "set", "plan_minutes", "500"},
		{"config", "set", "plan_minutes", "soon"},
		{"config", "set", "nope", "1"},
		{"config", "protect", "relative"},
		{"config", "unprotect", "~/Documents"},
	} {
		if _, err := run(t, bad...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if _, err := run(t, "config", "unprotect", "~/code/a"); err != nil {
		t.Fatal(err)
	}
	if c, err := config.Load(home); err != nil || len(c.Protect) != 0 || c.Plan != 45 {
		t.Errorf("after unprotect: %+v, %v", c, err)
	}
	out, _ = run(t, "config", "path")
	if strings.TrimSpace(out) != config.Path(home) {
		t.Errorf("path = %q", out)
	}
}
