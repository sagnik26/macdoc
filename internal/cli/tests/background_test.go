package tests

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/engine"
)

// Lowering the process to background priority is for a scan, which should not
// make the Mac sluggish. It used to happen for every overview, including the
// cached one that runs no scan, and made that read about four times slower
// (6.3 s against 1.5 s measured). It happens at the scan step and nowhere else.
func TestBackgroundOnlyWhenAScanStarts(t *testing.T) {
	calls := 0
	step := cli.Background(func() error { calls++; return nil }, &bytes.Buffer{})

	for _, s := range []engine.OverviewStep{engine.StepHealth, engine.StepLoad, engine.StepRank} {
		step(s)
	}
	if calls != 0 {
		t.Fatalf("priority lowered %d time(s) without a scan", calls)
	}
	step(engine.StepScan)
	if calls != 1 {
		t.Fatalf("priority lowered %d time(s) when a scan started, want 1", calls)
	}
}

func TestBackgroundSaysWhenItCannot(t *testing.T) {
	var w bytes.Buffer
	step := cli.Background(func() error { return errors.New("not permitted") }, &w)
	step(engine.StepHealth)
	if w.Len() != 0 {
		t.Errorf("output before any scan: %q", w.String())
	}
	step(engine.StepScan) // the scan still goes ahead; only a note is written
	if !strings.Contains(w.String(), "could not lower priority") || !strings.Contains(w.String(), "not permitted") {
		t.Errorf("note = %q", w.String())
	}
}
