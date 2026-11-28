package tests

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/scanner"
)

var escapes = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// plainText is s without escape sequences.
func plainText(s string) string { return escapes.ReplaceAllString(s, "") }

// The command-line status lines draw the progress bar in the Bubbles README's
// colors, in half height: lower-half blocks in the default purple-to-pink blend
// (#5A56E0 first), a dim track (#606060), and the percentage right after the bar.
func TestStatusBarIsTheBubblesReadmeBar(t *testing.T) {
	p := scanner.Progress{Bytes: 32_000_000_000, Files: 1000, Elapsed: 2 * time.Second}
	line := cli.ScanStatusLine(p, 128_000_000_000, "/Users/me")
	text := plainText(line)
	for _, want := range []string{"Scanning…", "█", " 25%", "32.0 GB", "1,000 files", "/Users/me"} {
		if want == "1,000 files" {
			want = "1000 files"
		}
		if !strings.Contains(text, want) {
			t.Errorf("%q missing from %q", want, text)
		}
	}
	if !strings.Contains(line, "38;2;90;86;224") { // #5A56E0, the start of the blend
		t.Errorf("the fill should start at the README's purple haze: %q", line)
	}
	if !strings.Contains(line, "38;2;96;96;96") { // #606060, the empty track
		t.Errorf("the track should be the README's slate gray: %q", line)
	}
}

// With no earlier scan the bar still shows, with no percentage; a scan past
// its estimate stops at 99%.
func TestScanStatusLineUnknownAndOverrun(t *testing.T) {
	p := scanner.Progress{Bytes: 32_000_000_000, Files: 1000, Elapsed: 2 * time.Second}
	unknown := plainText(cli.ScanStatusLine(p, 0, "/Users/me"))
	if !strings.Contains(unknown, "size unknown") || !strings.Contains(unknown, "█") || strings.Contains(unknown, "%") {
		t.Errorf("unknown size: %q", unknown)
	}
	if over := plainText(cli.ScanStatusLine(scanner.Progress{Bytes: 999}, 10, "")); !strings.Contains(over, " 99%") {
		t.Errorf("a scan past the estimate caps at 99%%: %q", over)
	}
}

// The retire and vitals lines use the same bar and count what has finished.
func TestSourceAndProbeStatusLines(t *testing.T) {
	src := plainText(cli.SourceStatusLine(3, 12, "homebrew"))
	for _, want := range []string{"Reading installed software", "█", " 25%", "3 of 12 sources", "homebrew"} {
		if !strings.Contains(src, want) {
			t.Errorf("%q missing from %q", want, src)
		}
	}
	if z := plainText(cli.SourceStatusLine(0, 0, "")); !strings.Contains(z, "0 of 0") {
		t.Errorf("no sources yet: %q", z)
	}
	probe := plainText(cli.ProbeStatusLine(3, 6, "network"))
	for _, want := range []string{"Reading your Mac", "█", " 50%", "3 of 6 readings", "network"} {
		if !strings.Contains(probe, want) {
			t.Errorf("%q missing from %q", want, probe)
		}
	}
}
