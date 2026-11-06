package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/quarantine"

	"github.com/sagnik26/macdoc/internal/store"
)

func writeEntry(t *testing.T, root string, m quarantine.Manifest, payload bool) {
	t.Helper()
	dir := filepath.Join(root, m.ID, quarantine.PayloadDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if payload {
		if err := os.MkdirAll(filepath.Join(dir, m.Name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(quarantine.ManifestPath(root, m.ID), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func manifest() quarantine.Manifest {
	return quarantine.Manifest{SchemaVersion: quarantine.SchemaVersion, ID: "Q-ABCD", Plan: "P-ABCD", Item: "O-x", Recipe: "user.logs",
		Original: "/Users/me/Library/Logs/A", Name: "A", CreatedAt: now, ExpiresAt: now.Add(quarantine.DefaultTTL), Status: quarantine.StatusDone}
}

func TestLoadManifestValidation(t *testing.T) {
	bad := map[string]func(m *quarantine.Manifest){
		"other id":        func(m *quarantine.Manifest) { m.ID = "Q-WXYZ" },
		"path name":       func(m *quarantine.Manifest) { m.Name = "../A" },
		"dot name":        func(m *quarantine.Manifest) { m.Name = ".." },
		"relative origin": func(m *quarantine.Manifest) { m.Original = "Library/Logs/A" },
		"name mismatch":   func(m *quarantine.Manifest) { m.Original = "/Users/me/Documents" },
		"unknown status":  func(m *quarantine.Manifest) { m.Status = "purged" },
		"schema":          func(m *quarantine.Manifest) { m.SchemaVersion = "9" },
	}
	for name, edit := range bad {
		root := t.TempDir()
		m := manifest()
		edit(&m)
		dir := filepath.Join(root, "Q-ABCD")
		_ = os.MkdirAll(dir, 0o700)
		b, _ := json.Marshal(m)
		_ = os.WriteFile(filepath.Join(dir, quarantine.ManifestName), b, 0o600)
		if _, err := quarantine.LoadManifest(root, "Q-ABCD"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	root := t.TempDir()
	writeEntry(t, root, manifest(), true)
	if _, err := quarantine.LoadManifest(root, "Q-ABCD"); err != nil {
		t.Fatal(err)
	}
	if _, err := quarantine.LoadManifest(root, "../x"); err == nil || !strings.Contains(err.Error(), "isn't a quarantine ID") {
		t.Errorf("path id = %v", err)
	}
	if err := os.WriteFile(quarantine.ManifestPath(root, "Q-ABCD"), []byte(`{"schema_version":"1"} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := quarantine.LoadManifest(root, "Q-ABCD"); err == nil {
		t.Error("trailing data accepted")
	}
}

func TestInspectStates(t *testing.T) {
	rec := func(st store.QuarantineStatus) store.QuarantineRecord {
		m := manifest()
		return store.QuarantineRecord{ID: m.ID, PlanID: m.Plan, Original: m.Original, ExpiresAt: m.ExpiresAt, Status: st}
	}
	cases := []struct {
		name    string
		setup   func(root string)
		status  store.QuarantineStatus
		want    quarantine.State
		expired bool
	}{
		{"held", func(r string) { writeEntry(t, r, manifest(), true) }, store.QuarantineDone, quarantine.StateHeld, false},
		{"interrupted", func(r string) {
			m := manifest()
			m.Status = quarantine.StatusPending
			writeEntry(t, r, m, true)
		}, store.QuarantinePending, quarantine.StateInterrupted, false},
		{"not moved", func(r string) {
			m := manifest()
			m.Status = quarantine.StatusPending
			writeEntry(t, r, m, false)
		}, store.QuarantinePending, quarantine.StateNotMoved, false},
		{"gone", func(string) {}, store.QuarantineDone, quarantine.StateMissing, false},
		{"payload gone", func(r string) { writeEntry(t, r, manifest(), false) }, store.QuarantineDone, quarantine.StateMissing, false},
		{"record mismatch", func(r string) {
			m := manifest()
			m.Plan = "P-ZZZZ"
			writeEntry(t, r, m, true)
		}, store.QuarantineDone, quarantine.StateDamaged, false},
		{"payload symlink", func(r string) {
			writeEntry(t, r, manifest(), false)
			_ = os.Symlink("/", filepath.Join(r, "Q-ABCD", quarantine.PayloadDir, "A"))
		}, store.QuarantineDone, quarantine.StateDamaged, false},
	}
	for _, c := range cases {
		root := t.TempDir()
		c.setup(root)
		e := quarantine.Inspect(root, rec(c.status), now)
		if e.State != c.want || e.Expired != c.expired {
			t.Errorf("%s: state %s (%s), want %s", c.name, e.State, e.Problem, c.want)
		}
	}
	root := t.TempDir()
	writeEntry(t, root, manifest(), true)
	if e := quarantine.Inspect(root, rec(store.QuarantineDone), now.Add(8*24*time.Hour)); !e.Expired || !e.Restorable() {
		t.Errorf("expired entry = %+v", e)
	}
}
