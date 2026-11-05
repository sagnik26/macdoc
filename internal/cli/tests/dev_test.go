package tests

import (
	"encoding/json"
	"macdoc/internal/cli"
	"strings"
	"testing"
)

func TestDevCommand(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "dev")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "macdoc doctor dev · scan S-", " node ─", "18.0 GB", " system ─", "5 × Stale node_modules folder", "nothing has been changed")
	if strings.Index(out, " node ─") > strings.Index(out, " system ─") {
		t.Error("biggest toolchain first")
	}

	// One toolchain lists every item.
	out, err = run(t, "dev", "node")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Stale node_modules folder · a", "→ npm cache clean --force")
	if strings.Contains(out, " system ─") || strings.Contains(out, "5 ×") {
		t.Errorf("dev node:\n%s", out)
	}

	out, err = run(t, "dev", "brew")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Nothing for homebrew in the latest scan.")

	_, err = run(t, "dev", "cobol")
	if err == nil || !strings.Contains(err.Error(), `unknown toolchain "cobol"; valid: xcode, cocoapods, android`) {
		t.Errorf("err = %v", err)
	}

	out, err = run(t, "dev", "system", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string        `json:"command"`
		Data    cli.DevResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "dev" || env.Data.Toolchain != "system" || len(env.Data.Toolchains) != 1 || len(env.Data.Toolchains[0].Opportunities) != 1 {
		t.Errorf("envelope = %+v", env)
	}
}

func TestRenderDevEmpty(t *testing.T) {
	var b strings.Builder
	if err := cli.RenderDev(&b, cli.DevResult{Warnings: []string{"x: y"}}, nil); err != nil {
		t.Fatal(err)
	}
	mustContain(t, b.String(), "Nothing to reclaim was found", " Notes\n  · x: y")
}
