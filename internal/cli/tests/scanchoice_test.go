package tests

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

func askAtATerminal(t *testing.T, on bool) {
	t.Helper()
	old := cli.ScanAsk
	cli.ScanAsk = func(*cobra.Command) bool { return on }
	t.Cleanup(func() { cli.ScanAsk = old })
}

// In a terminal, `scan` asks, and "u" keeps the last data: the report is
// the stored one (a 2-hour-old scan), nothing is rescanned.
func TestScanAsksAndUsesTheLastData(t *testing.T) {
	cliFixture(t)
	askAtATerminal(t, true)
	out, err := runWithInput(t, "u\n", "scan")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Last scan: 2h ago", "[u]se last", "scan 2h ago")
}

// --last and --fresh skip the question; both together is an error; a wrong
// or missing answer scans nothing.
func TestScanFlagsAndBadAnswers(t *testing.T) {
	cliFixture(t)
	askAtATerminal(t, true)
	out, err := runWithInput(t, "", "scan", "--last")
	if err != nil || strings.Contains(out, "Scan the disk again") {
		t.Fatalf("--last should not ask: %v\n%s", err, out)
	}
	mustContain(t, out, "scan 2h ago")
	if _, err := runWithInput(t, "", "scan", "--fresh", "--last"); err == nil {
		t.Error("--fresh with --last should be refused")
	}
	if _, err := runWithInput(t, "maybe\n", "scan"); err == nil || !strings.Contains(err.Error(), "answer f") {
		t.Errorf("a bad answer: err = %v", err)
	}
	if _, err := runWithInput(t, "", "scan"); err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Errorf("no answer: err = %v", err)
	}
}

// Piped, or with --json or --quiet, it never asks (the scheduled scan relies
// on that).
func TestScanDoesNotAskWhenNotInteractive(t *testing.T) {
	cliFixture(t)
	askAtATerminal(t, false)
	for _, args := range [][]string{{"scan", "--last"}, {"scan", "--last", "--json"}, {"scan", "--last", "--quiet"}} {
		out, err := runWithInput(t, "", args...)
		if err != nil || strings.Contains(out, "Scan the disk again") {
			t.Errorf("%v asked or failed: %v\n%s", args, err, out)
		}
	}
	askAtATerminal(t, true)
	out, err := runWithInput(t, "", "scan", "--last", "--json")
	if err != nil || strings.Contains(out, "Scan the disk again") {
		t.Errorf("--json must never ask: %v\n%s", err, out)
	}
}
