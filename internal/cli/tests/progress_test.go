package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/scanner"
)

// The command-line scan line always has a bar: a percentage with an expected
// size, "size unknown" without one (never a fake 0%).
func TestScanStatusLineAlwaysHasABar(t *testing.T) {
	p := scanner.Progress{Bytes: 32_000_000_000, Files: 1000, Elapsed: 2 * time.Second}
	known := cli.ScanStatusLine(p, 128_000_000_000, "/Users/me")
	if !strings.Contains(known, "~25%") || !strings.Contains(known, "█") || !strings.Contains(known, "░") {
		t.Errorf("known size: %q", known)
	}
	unknown := cli.ScanStatusLine(p, 0, "/Users/me")
	if !strings.Contains(unknown, "size unknown") || !strings.Contains(unknown, "[") || strings.Contains(unknown, "%") {
		t.Errorf("unknown size: %q", unknown)
	}
	if over := cli.ScanStatusLine(scanner.Progress{Bytes: 999}, 10, ""); !strings.Contains(over, "~99%") {
		t.Errorf("a scan past the estimate caps at 99%%: %q", over)
	}
}

// The retire status line always carries a bar and counts the sources read.
func TestSourceStatusLine(t *testing.T) {
	got := cli.SourceStatusLine(3, 12, "homebrew")
	for _, want := range []string{"Reading installed software", "[██████░░░░░░░░░░░░░░░░░░]", "3 of 12 sources", "homebrew"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	if z := cli.SourceStatusLine(0, 0, ""); !strings.Contains(z, "0 of 0") {
		t.Errorf("no sources yet: %q", z)
	}
}

// The vitals status line carries a bar and counts the readings in.
func TestProbeStatusLine(t *testing.T) {
	got := cli.ProbeStatusLine(3, 6, "network")
	for _, want := range []string{"Reading your Mac", "[████████████░░░░░░░░░░░░]", "3 of 6 readings", "network"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
}
