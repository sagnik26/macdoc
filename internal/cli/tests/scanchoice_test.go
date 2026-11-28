package tests

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
)

// runWithInput runs the CLI with text typed at its prompts.
func runWithInput(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	cmd := cli.NewRootCmd()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// `scan` has no "use the last scan" option: it never asks, and --last and
// --fresh no longer exist.
func TestScanHasNoLastScanOption(t *testing.T) {
	cliFixture(t)
	for _, flag := range []string{"--last", "--fresh"} {
		if _, err := runWithInput(t, "", "scan", flag); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("scan %s: err = %v, want unknown flag", flag, err)
		}
	}
}
