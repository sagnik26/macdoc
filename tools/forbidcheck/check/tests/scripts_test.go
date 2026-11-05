package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// These tests run the guard scripts end to end so `make test` proves they
// work (and that importcheck really fails on violations, via --self-test).
// They invoke bash explicitly so they do not depend on the executable bit.

func runScript(t *testing.T, args ...string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("runs go list / go run; skipped with -short")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	out, err := exec.Command(bash, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestImportcheckScript(t *testing.T) {
	out := runScript(t, "../../../../scripts/importcheck.sh")
	if !strings.Contains(out, "importcheck: ok") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestImportcheckSelfTest(t *testing.T) {
	t.Setenv("IMPORTCHECK_VERBOSE", "1")
	out := runScript(t, "../../../../scripts/importcheck.sh", "--self-test")
	if !strings.Contains(out, "importcheck self-test: ok") {
		t.Errorf("unexpected output:\n%s", out)
	}
	t.Log(out) // each violation's report; visible with go test -v
}

func TestForbidcheckScript(t *testing.T) {
	runScript(t, "../../../../scripts/forbidcheck.sh")
}
