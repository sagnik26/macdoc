package tests

import (
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
)

func TestDryRunRoundTrip(t *testing.T) {
	s := openTemp(t)
	if _, ok, err := s.DryRun("brew autoremove -n"); ok || err != nil {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	in := store.DryRun{Key: "brew autoremove -n", Fingerprint: "abc", At: t0, ExitCode: 0,
		Stdout: []byte("==> Would autoremove 1 unneeded formula:\nlibfoo\n"), Stderr: nil}
	if err := s.SaveDryRun(in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.DryRun(in.Key)
	if err != nil || !ok || got.Fingerprint != "abc" || !got.At.Equal(t0) || string(got.Stdout) != string(in.Stdout) || len(got.Stderr) != 0 {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	in.Fingerprint = "def" // the same key is replaced, not duplicated
	if err := s.SaveDryRun(in); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM dryruns`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d, %v; want 1", n, err)
	}
	if got, _, _ = s.DryRun(in.Key); got.Fingerprint != "def" {
		t.Errorf("fingerprint %q after replace", got.Fingerprint)
	}
}

// A database from the release before the cache opens and gains the table
// without losing its rows.
func TestDryRunUpgradeAddsTable(t *testing.T) {
	saved := store.Migrations
	t.Cleanup(func() { store.Migrations = saved })
	path := filepath.Join(t.TempDir(), "state.db")
	store.Migrations = saved[:5] // the schema before the cache
	old, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.DB.Exec(`SELECT 1 FROM dryruns`); err == nil {
		t.Fatal("fixture is wrong: the old schema already has dryruns")
	}
	id := mustBegin(t, old, t0)
	mustAdd(t, old, id, dir("/a", 1), dir("/b", 2))
	mustFinish(t, old, id, model.ScanComplete, t0)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	store.Migrations = saved
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open with the new schema: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.SaveDryRun(store.DryRun{Key: "k", At: t0}); err != nil {
		t.Fatalf("table missing after upgrade: %v", err)
	}
	if ns, err := s.Nodes(id, store.Query{}); err != nil || len(ns) != 2 {
		t.Errorf("old rows after upgrade: %v, %v", ns, err)
	}
	var version int
	if err := s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != len(saved) {
		t.Errorf("schema version %d, %v; want %d", version, err, len(saved))
	}
}
