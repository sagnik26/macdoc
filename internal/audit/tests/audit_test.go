package tests

import (
	"github.com/sagnik26/macdoc/internal/audit"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppendRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", audit.FileName)
	n := int64(42)
	for i, ev := range []string{audit.ApplyStarted, audit.ItemDone, audit.ApplyFinished} {
		rec := audit.Record{TS: time.Unix(int64(i), 0), Event: ev, Plan: "P-ABCD"}
		if ev == audit.ApplyFinished {
			rec.ActualBytes = &n
		}
		if err := audit.Append(p, rec); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	recs, bad, err := audit.Read(p, 0)
	if err != nil || bad != 0 || len(recs) != 3 {
		t.Fatalf("Read = %d records, %d bad, %v", len(recs), bad, err)
	}
	if recs[2].ActualBytes == nil || *recs[2].ActualBytes != 42 || recs[1].ActualBytes != nil {
		t.Errorf("actual bytes not kept: %+v", recs)
	}
	last, _, _ := audit.Read(p, 2)
	if len(last) != 2 || last[0].Event != audit.ItemDone {
		t.Errorf("limit 2 = %+v", last)
	}
}

func TestReadMissing(t *testing.T) {
	recs, bad, err := audit.Read(filepath.Join(t.TempDir(), "nope.jsonl"), 0)
	if err != nil || bad != 0 || len(recs) != 0 {
		t.Errorf("missing log = %v %d %v", recs, bad, err)
	}
}

// A crash mid-write leaves a torn line: reading skips it and the next
// append starts on a fresh line.
func TestTornLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), audit.FileName)
	if err := audit.Append(p, audit.Record{Event: audit.ApplyStarted, Plan: "P-ABCD"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ts":"2026-01-01T00:00:00Z","event":"item_do`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := audit.Append(p, audit.Record{Event: audit.ApplyFinished, Plan: "P-ABCD"}); err != nil {
		t.Fatal(err)
	}
	recs, bad, err := audit.Read(p, 0)
	if err != nil || bad != 1 || len(recs) != 2 || recs[1].Event != audit.ApplyFinished {
		t.Fatalf("Read = %+v, bad %d, %v", recs, bad, err)
	}
}

func TestConcurrentAppends(t *testing.T) {
	p := filepath.Join(t.TempDir(), audit.FileName)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := audit.Append(p, audit.Record{Event: audit.ItemDone, Output: strings.Repeat("x", 8000)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	recs, bad, err := audit.Read(p, 0)
	if err != nil || bad != 0 || len(recs) != 20 {
		t.Fatalf("Read = %d, bad %d, %v", len(recs), bad, err)
	}
}

func TestTail(t *testing.T) {
	if audit.Tail([]byte("short")) != "short" {
		t.Error("short output changed")
	}
	long := strings.Repeat("a", audit.MaxOutput) + "END"
	if got := audit.Tail([]byte(long)); !strings.HasSuffix(got, "END") || !strings.HasPrefix(got, "…") || len(got) > audit.MaxOutput+len("…") {
		t.Errorf("Tail kept %d bytes", len(got))
	}
}
