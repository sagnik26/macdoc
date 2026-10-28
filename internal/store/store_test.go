package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"macdoc/internal/model"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustBegin(t *testing.T, s *Store, at time.Time) string {
	t.Helper()
	id, err := s.BeginScan("/", "test", at)
	if err != nil {
		t.Fatalf("BeginScan: %v", err)
	}
	return id
}

func mustAdd(t *testing.T, s *Store, id string, nodes ...model.Node) {
	t.Helper()
	if err := s.AddNodes(id, nodes); err != nil {
		t.Fatalf("AddNodes: %v", err)
	}
}

func mustFinish(t *testing.T, s *Store, id string, st model.ScanStatus, at time.Time) {
	t.Helper()
	if err := s.FinishScan(id, st, 0, 0, at); err != nil {
		t.Fatalf("FinishScan: %v", err)
	}
}

func dir(p string, b int64) model.Node {
	return model.Node{Path: p, Kind: model.NodeDir, Bytes: b, Files: 1, MtimeMax: 1, Depth: depthOf(p)}
}

func paths(ns []model.Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Path
	}
	return out
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestDefaultPath(t *testing.T) {
	if got := DefaultPath("/Users/x"); got != "/Users/x/Library/Application Support/macdoc/state.db" {
		t.Errorf("DefaultPath = %q", got)
	}
}

func TestOpenModesAndIdempotent(t *testing.T) {
	home := t.TempDir()
	p := DefaultPath(home) // has a space: exercises URI escaping
	s, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := mode(t, filepath.Dir(p)); got != 0o700 {
		t.Errorf("dir mode = %o, want 700", got)
	}
	if got := mode(t, p); got != 0o600 {
		t.Errorf("file mode = %o, want 600", got)
	}
	id := mustBegin(t, s, t0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(p, 0o644); err != nil { //nolint:gosec // G302: loosened on purpose to check that Open tightens it

		t.Fatal(err)
	}
	s, err = Open(p)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	if got := mode(t, p); got != 0o600 {
		t.Errorf("tightened mode = %o, want 600", got)
	}
	if _, err := s.Scan(id); err != nil {
		t.Errorf("scan lost on re-open: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n != len(migrations) {
		t.Errorf("schema_migrations rows = %d, %v; want %d", n, err, len(migrations))
	}
	var jm string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&jm); err != nil || jm != "wal" {
		t.Errorf("journal_mode = %q, %v", jm, err)
	}
	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d, %v", fk, err)
	}
}

func TestOpenTightPermsKept(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(p, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	// 0400 is not looser than 0600, so it is left alone (and the open fails
	// because the file is read-only for us).
	if s, err := Open(p); err == nil {
		_ = s.Close()
		t.Fatal("Open of a read-only file succeeded")
	}
	if got := mode(t, p); got != 0o400 {
		t.Errorf("mode = %o, want 400 unchanged", got)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Error("Open(\"\") succeeded")
	}
	tmp := t.TempDir()
	file := filepath.Join(tmp, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(file, "sub", "state.db")); err == nil {
		t.Error("Open below a regular file succeeded")
	}
	if _, err := Open(tmp); err == nil {
		t.Error("Open of a directory succeeded")
	}
	garbage := filepath.Join(tmp, "garbage.db")
	if err := os.WriteFile(garbage, []byte(strings.Repeat("not a database ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(garbage); err == nil {
		t.Error("Open of a non-database file succeeded")
	}
}

func TestOpenRejectsFutureSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO schema_migrations VALUES (?, 0)`, len(migrations)+1); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_, err = Open(p)
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Open with future schema: err = %v", err)
	}
}

func TestMigrationFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = append(append([]string{}, saved...), `THIS IS NOT SQL`)
	if _, err := Open(p); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("migration %d", len(saved)+1)) {
		t.Fatalf("bad migration: err = %v", err)
	}
	migrations = saved
	s, err = Open(p)
	if err != nil {
		t.Fatalf("re-Open after failed migration: %v", err)
	}
	_ = s.Close()
}

func TestLifecycle(t *testing.T) {
	s := openTemp(t)
	id, err := s.BeginScan("/Users/a", "1.2.3", t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 10 || !strings.HasPrefix(id, "S-") || strings.ToUpper(id) != id {
		t.Errorf("bad ID %q", id)
	}
	info, err := s.Scan(id)
	if err != nil {
		t.Fatal(err)
	}
	want := model.ScanInfo{ID: id, Root: "/Users/a", StartedAt: t0, Status: model.ScanRunning, MacdocVersion: "1.2.3"}
	if !reflect.DeepEqual(info, want) {
		t.Errorf("running scan = %+v, want %+v", info, want)
	}

	a := model.Node{Path: "/Users/a/big", Kind: model.NodeDir, Bytes: 100, Files: 10, MtimeMax: 1700, Depth: 1}
	b := model.Node{Path: "/Users/a/file.bin", Kind: model.NodeFile, Bytes: 50, Files: 1, MtimeMax: 1600, Depth: 1}
	c := model.Node{Path: "/Users/a/big/x", Kind: model.NodeDir, Bytes: 30, Files: 3, MtimeMax: 1500, Depth: 2}
	mustAdd(t, s, id, a, b)
	mustAdd(t, s, id, c)
	mustAdd(t, s, id) // empty batch is fine
	a2 := a
	a2.Path, a2.Bytes = "/Users/a/BIG", 120 // same path_fold: upsert
	mustAdd(t, s, id, a2)

	if err := s.AddUnmeasured(id, []model.Unmeasured{
		{Path: "/Users/a/z", Reason: "permission denied"},
		{Path: "/Users/a/m", Reason: "permission denied"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUnmeasured(id, []model.Unmeasured{{Path: "/Users/a/b", Reason: "io error"}}); err != nil {
		t.Fatal(err)
	}

	fin := t0.Add(time.Minute)
	if err := s.FinishScan(id, model.ScanComplete, 999, 42, fin); err != nil {
		t.Fatal(err)
	}
	info, err = s.Scan(id)
	if err != nil {
		t.Fatal(err)
	}
	want.FinishedAt, want.Status, want.TotalBytes, want.TotalFiles, want.UnmeasuredCount = fin, model.ScanComplete, 999, 42, 3
	if !reflect.DeepEqual(info, want) {
		t.Errorf("finished scan = %+v, want %+v", info, want)
	}
	latest, err := s.LatestScan("")
	if err != nil || latest.ID != id {
		t.Errorf("LatestScan = %+v, %v", latest, err)
	}

	nodes, err := s.Nodes(id, NodeQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if wantN := []model.Node{a2, b, c}; !reflect.DeepEqual(nodes, wantN) {
		t.Errorf("Nodes = %+v, want %+v", nodes, wantN)
	}
	um, err := s.Unmeasured(id)
	if err != nil {
		t.Fatal(err)
	}
	wantU := []model.Unmeasured{
		{Path: "/Users/a/b", Reason: "io error"},
		{Path: "/Users/a/m", Reason: "permission denied"},
		{Path: "/Users/a/z", Reason: "permission denied"},
	}
	if !reflect.DeepEqual(um, wantU) {
		t.Errorf("Unmeasured = %+v", um)
	}
}

func TestEmptyResultsAreNonNil(t *testing.T) {
	s := openTemp(t)
	id := mustBegin(t, s, t0)
	if n, err := s.Nodes(id, NodeQuery{}); err != nil || n == nil || len(n) != 0 {
		t.Errorf("Nodes = %#v, %v", n, err)
	}
	if u, err := s.Unmeasured(id); err != nil || u == nil || len(u) != 0 {
		t.Errorf("Unmeasured = %#v, %v", u, err)
	}
	if h, err := s.History("/x", 0); err != nil || h == nil || len(h) != 0 {
		t.Errorf("History = %#v, %v", h, err)
	}
}

func TestUnknownScan(t *testing.T) {
	s := openTemp(t)
	checks := map[string]error{}
	_, checks["Scan"] = s.Scan("S-NOPE0000")
	_, checks["Nodes"] = s.Nodes("S-NOPE0000", NodeQuery{})
	_, checks["Unmeasured"] = s.Unmeasured("S-NOPE0000")
	checks["AddNodes"] = s.AddNodes("S-NOPE0000", nil)
	checks["AddUnmeasured"] = s.AddUnmeasured("S-NOPE0000", nil)
	checks["FinishScan"] = s.FinishScan("S-NOPE0000", model.ScanComplete, 0, 0, t0)
	for name, err := range checks {
		if !errors.Is(err, ErrNoScan) {
			t.Errorf("%s: err = %v, want ErrNoScan", name, err)
		}
	}
}

func TestLatestScanByStatus(t *testing.T) {
	s := openTemp(t)
	if _, err := s.LatestScan(""); !errors.Is(err, ErrNoScan) {
		t.Fatalf("empty db: err = %v", err)
	}
	c1 := mustBegin(t, s, t0)
	mustFinish(t, s, c1, model.ScanComplete, t0)
	inc := mustBegin(t, s, t0.Add(time.Hour))
	mustFinish(t, s, inc, model.ScanIncomplete, t0.Add(time.Hour))
	run := mustBegin(t, s, t0.Add(2*time.Hour))

	for st, want := range map[model.ScanStatus]string{
		"":                   run,
		model.ScanRunning:    run,
		model.ScanComplete:   c1,
		model.ScanIncomplete: inc,
	} {
		got, err := s.LatestScan(st)
		if err != nil || got.ID != want {
			t.Errorf("LatestScan(%q) = %s, %v; want %s", st, got.ID, err, want)
		}
	}
	// Ties on started_at go to the later insert.
	tie := mustBegin(t, s, t0.Add(2*time.Hour))
	if got, _ := s.LatestScan(""); got.ID != tie {
		t.Errorf("tie: LatestScan = %s, want %s", got.ID, tie)
	}
}

func TestFinishedScanIsReadOnly(t *testing.T) {
	s := openTemp(t)
	id := mustBegin(t, s, t0)
	if err := s.FinishScan(id, model.ScanRunning, 0, 0, t0); err == nil {
		t.Error("FinishScan(running) succeeded")
	}
	if err := s.FinishScan(id, "bogus", 0, 0, t0); err == nil {
		t.Error("FinishScan(bogus) succeeded")
	}
	mustFinish(t, s, id, model.ScanIncomplete, t0)
	if err := s.AddNodes(id, []model.Node{dir("/a", 1)}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("AddNodes on finished: %v", err)
	}
	if err := s.AddUnmeasured(id, []model.Unmeasured{{Path: "/a"}}); err == nil {
		t.Error("AddUnmeasured on finished succeeded")
	}
	if err := s.FinishScan(id, model.ScanComplete, 0, 0, t0); err == nil {
		t.Error("second FinishScan succeeded")
	}
	if n, _ := s.Nodes(id, NodeQuery{}); len(n) != 0 {
		t.Errorf("nodes were added: %v", n)
	}
}

func TestNodesQueries(t *testing.T) {
	s := openTemp(t)
	id := mustBegin(t, s, t0)
	nfc := "/Users/a/Café"  // é precomposed
	nfd := "/users/a/café" // e + combining acute
	mustAdd(t, s, id,
		dir("/Users", 1000),
		dir("/Users/a", 500),
		dir("/Users/a/Library", 300),
		dir("/Users/a/Library/Caches", 200),
		dir("/Users/a/Library/Caches/deep", 100),
		dir("/Users/ab", 400),
		dir("/Users/ab/x", 50),
		dir(nfc, 60),
		dir("/Users/a%b", 70),
		dir("/Users/a%b/c", 7),
		dir("/Users/aXb", 80),
		dir("/Users/a_b", 90),
		dir("/Users/a_b/d", 9),
		dir("/Users/zz", 100), // ties with .../deep on bytes
		dir(`/Users/q\r`, 5),
		dir(`/Users/q\r/s`, 4),
		dir(`/Users/qXr`, 3),
	)

	cases := []struct {
		name string
		q    NodeQuery
		want []string
	}{
		{"component-wise", NodeQuery{Prefix: "/Users/a"}, []string{
			"/Users/a", "/Users/a/Library", "/Users/a/Library/Caches", "/Users/a/Library/Caches/deep", nfc}},
		{"trailing slash", NodeQuery{Prefix: "/Users/ab/"}, []string{"/Users/ab", "/Users/ab/x"}},
		{"case-insensitive", NodeQuery{Prefix: "/USERS/A/library"}, []string{
			"/Users/a/Library", "/Users/a/Library/Caches", "/Users/a/Library/Caches/deep"}},
		{"NFD against NFC", NodeQuery{Prefix: nfd}, []string{nfc}},
		{"percent literal", NodeQuery{Prefix: "/Users/a%b"}, []string{"/Users/a%b", "/Users/a%b/c"}},
		{"underscore literal", NodeQuery{Prefix: "/Users/a_b"}, []string{"/Users/a_b", "/Users/a_b/d"}},
		{"backslash literal", NodeQuery{Prefix: `/Users/q\r`}, []string{`/Users/q\r`, `/Users/q\r/s`}},
		{"no match", NodeQuery{Prefix: "/Nope"}, []string{}},
		{"min bytes", NodeQuery{MinBytes: 300}, []string{"/Users", "/Users/a", "/Users/ab", "/Users/a/Library"}},
		{"max depth from root", NodeQuery{Prefix: "/", MaxDepth: 1}, []string{"/Users"}},
		{"max depth relative", NodeQuery{Prefix: "/Users/a", MaxDepth: 1}, []string{
			"/Users/a", "/Users/a/Library", nfc}},
		{"max depth 2", NodeQuery{Prefix: "/users/a", MaxDepth: 2, MinBytes: 150}, []string{
			"/Users/a", "/Users/a/Library", "/Users/a/Library/Caches"}},
		{"limit", NodeQuery{Limit: 2}, []string{"/Users", "/Users/a"}},
		{"tie-break by path", NodeQuery{MinBytes: 100, Limit: 7}, []string{
			"/Users", "/Users/a", "/Users/ab", "/Users/a/Library", "/Users/a/Library/Caches",
			"/Users/a/Library/Caches/deep", "/Users/zz"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Nodes(id, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(paths(got), tc.want) {
				t.Errorf("got %q\nwant %q", paths(got), tc.want)
			}
		})
	}

	all, err := s.Nodes(id, NodeQuery{Prefix: "/"})
	if err != nil || len(all) != 17 {
		t.Errorf(`Prefix "/" returned %d nodes, %v`, len(all), err)
	}
}

func TestNodesAreScopedToScan(t *testing.T) {
	s := openTemp(t)
	a := mustBegin(t, s, t0)
	b := mustBegin(t, s, t0)
	mustAdd(t, s, a, dir("/x", 1))
	mustAdd(t, s, b, dir("/x", 2), dir("/y", 3))
	got, _ := s.Nodes(a, NodeQuery{})
	if len(got) != 1 || got[0].Bytes != 1 {
		t.Errorf("scan a nodes = %+v", got)
	}
}

func TestHistory(t *testing.T) {
	s := openTemp(t)
	var complete []string
	for i, st := range []model.ScanStatus{model.ScanComplete, model.ScanIncomplete, model.ScanComplete, model.ScanComplete} {
		at := t0.Add(time.Duration(i) * time.Hour)
		id := mustBegin(t, s, at)
		mustAdd(t, s, id, dir("/Users/a/Library", int64(100*(i+1))), dir("/Users/a", 1))
		mustFinish(t, s, id, st, at)
		if st == model.ScanComplete {
			complete = append(complete, id)
		}
	}
	running := mustBegin(t, s, t0.Add(10*time.Hour))
	mustAdd(t, s, running, dir("/Users/a/Library", 9999))

	h, err := s.History("/users/A/library/", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []HistoryPoint{
		{complete[0], t0, 100},
		{complete[1], t0.Add(2 * time.Hour), 300},
		{complete[2], t0.Add(3 * time.Hour), 400},
	}
	if !reflect.DeepEqual(h, want) {
		t.Errorf("History = %+v\nwant %+v", h, want)
	}
	h, err = s.History("/Users/a/Library", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h, want[1:]) {
		t.Errorf("History limit 2 = %+v\nwant %+v", h, want[1:])
	}
	if h, _ := s.History("/Users", 0); len(h) != 0 {
		t.Errorf("History of unstored path = %+v", h)
	}
}

func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPruneScans(t *testing.T) {
	s := openTemp(t)
	if err := s.PruneScans(-1); err == nil {
		t.Error("PruneScans(-1) succeeded")
	}
	if err := s.PruneScans(3); err != nil { // empty db
		t.Fatal(err)
	}
	mk := func(i int, st model.ScanStatus) string {
		at := t0.Add(time.Duration(i) * time.Hour)
		id := mustBegin(t, s, at)
		mustAdd(t, s, id, dir("/a", 1), dir("/a/b", 1))
		if err := s.AddUnmeasured(id, []model.Unmeasured{{Path: "/u", Reason: "r"}}); err != nil {
			t.Fatal(err)
		}
		if st != model.ScanRunning {
			mustFinish(t, s, id, st, at)
		}
		return id
	}
	c0 := mk(0, model.ScanComplete)
	i1 := mk(1, model.ScanIncomplete)
	r2 := mk(2, model.ScanRunning) // old running scan: untouched
	c3 := mk(3, model.ScanComplete)
	c4 := mk(4, model.ScanComplete)
	i5 := mk(5, model.ScanIncomplete) // newer than newest complete: kept
	r6 := mk(6, model.ScanRunning)

	if err := s.PruneScans(2); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{c0, i1} {
		if _, err := s.Scan(id); !errors.Is(err, ErrNoScan) {
			t.Errorf("%s survived prune: %v", id, err)
		}
	}
	for _, id := range []string{r2, c3, c4, i5, r6} {
		if _, err := s.Scan(id); err != nil {
			t.Errorf("%s removed: %v", id, err)
		}
	}
	if n := count(t, s, "nodes"); n != 10 {
		t.Errorf("nodes rows = %d, want 10 (cascade)", n)
	}
	if n := count(t, s, "unmeasured"); n != 5 {
		t.Errorf("unmeasured rows = %d, want 5 (cascade)", n)
	}

	// keep=0 drops every complete scan and incomplete scans older than the
	// newest complete one; running scans stay.
	if err := s.PruneScans(0); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, "scans"); n != 3 { // r2, i5, r6
		t.Errorf("scans after keep=0: %d, want 3", n)
	}
}

func TestBeginScanIDCollision(t *testing.T) {
	s := openTemp(t)
	saved := randRead
	t.Cleanup(func() { randRead = saved })
	randRead = func(b []byte) (int, error) {
		for i := range b {
			b[i] = 0
		}
		return len(b), nil
	}
	first := mustBegin(t, s, t0)
	if first != "S-AAAAAAAA" {
		t.Errorf("ID = %q", first)
	}
	// Every retry collides.
	if _, err := s.BeginScan("/", "v", t0); err == nil || !strings.Contains(err.Error(), "unique") {
		t.Errorf("collision: err = %v", err)
	}
	// One collision, then a fresh ID.
	calls := 0
	randRead = func(b []byte) (int, error) {
		calls++
		var fill byte // zeros collide with first; later calls are fresh
		if calls > 1 {
			fill = 1
		}
		for i := range b {
			b[i] = fill
		}
		return len(b), nil
	}
	id, err := s.BeginScan("/", "v", t0)
	if err != nil || id == first || calls != 2 {
		t.Errorf("retry: id=%q err=%v calls=%d", id, err, calls)
	}
	randRead = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, err := s.BeginScan("/", "v", t0); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("rand failure: err = %v", err)
	}
}

func TestClosedStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	id := mustBegin(t, s, t0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginScan("/", "v", t0); err == nil {
		t.Error("BeginScan after Close succeeded")
	}
	if err := s.AddNodes(id, nil); err == nil {
		t.Error("AddNodes after Close succeeded")
	}
	if _, err := s.Scan(id); err == nil || errors.Is(err, ErrNoScan) {
		t.Errorf("Scan after Close: %v", err)
	}
	if _, err := s.Nodes(id, NodeQuery{}); err == nil {
		t.Error("Nodes after Close succeeded")
	}
	if _, err := s.Unmeasured(id); err == nil {
		t.Error("Unmeasured after Close succeeded")
	}
	if _, err := s.History("/", 0); err == nil {
		t.Error("History after Close succeeded")
	}
	if err := s.PruneScans(1); err == nil {
		t.Error("PruneScans after Close succeeded")
	}
}

func TestConcurrentReadersAndWriter(t *testing.T) {
	s := openTemp(t)
	id := mustBegin(t, s, t0)
	const batches, per = 20, 200
	var wg sync.WaitGroup
	errc := make(chan error, 64)
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for b := range batches {
			ns := make([]model.Node, per)
			for i := range ns {
				ns[i] = dir(fmt.Sprintf("/r/%d/%d", b, i), int64(b*per+i))
			}
			if err := s.AddNodes(id, ns); err != nil {
				errc <- err
				return
			}
		}
	}()
	// A second writer on another scan contends for the write lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 10 {
			other, err := s.BeginScan("/", "v", t0)
			if err == nil {
				err = s.FinishScan(other, model.ScanComplete, 0, 0, t0)
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				ns, err := s.Nodes(id, NodeQuery{Prefix: "/r"})
				if err != nil {
					errc <- err
					return
				}
				// Batches commit atomically: counts are whole batches and
				// never go backwards.
				if len(ns)%per != 0 || len(ns) < last {
					errc <- fmt.Errorf("saw %d nodes after %d", len(ns), last)
					return
				}
				last = len(ns)
				if _, err := s.LatestScan(""); err != nil {
					errc <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Error(err)
	}
	ns, err := s.Nodes(id, NodeQuery{})
	if err != nil || len(ns) != batches*per {
		t.Errorf("final nodes = %d, %v", len(ns), err)
	}
}

func TestTwoHandlesSameFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	id := mustBegin(t, a, t0)
	mustAdd(t, b, id, dir("/x", 1))
	if ns, err := a.Nodes(id, NodeQuery{}); err != nil || len(ns) != 1 {
		t.Errorf("cross-handle read = %v, %v", ns, err)
	}
}

func TestHelpers(t *testing.T) {
	for p, want := range map[string]int{"/": 0, "": 0, "/a": 1, "/a/b/": 2, "a/b": 2} {
		if got := depthOf(p); got != want {
			t.Errorf("depthOf(%q) = %d, want %d", p, got, want)
		}
	}
	if got := escapeLike(`a%b_c\d`); got != `a\%b\_c\\d` {
		t.Errorf("escapeLike = %q", got)
	}
	if fold("/A/Café") != "/a/café" {
		t.Error("fold did not NFC + lowercase")
	}
}

// TestDatabaseErrors injects failures with SQLite triggers and malformed rows
// to cover the error paths of each write and read.
func TestDatabaseErrors(t *testing.T) {
	s := openTemp(t)
	exec := func(q string) {
		t.Helper()
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TRIGGER fail_node BEFORE INSERT ON nodes WHEN NEW.path = '/boom'
		BEGIN SELECT RAISE(ABORT, 'node boom'); END`)
	exec(`CREATE TRIGGER fail_unmeasured BEFORE INSERT ON unmeasured WHEN NEW.path = '/boom'
		BEGIN SELECT RAISE(ABORT, 'unmeasured boom'); END`)
	exec(`CREATE TRIGGER fail_finish BEFORE UPDATE ON scans WHEN NEW.total_files = 666
		BEGIN SELECT RAISE(ABORT, 'finish boom'); END`)

	id := mustBegin(t, s, t0)
	if err := s.AddNodes(id, []model.Node{dir("/ok", 1), dir("/boom", 1)}); err == nil || !strings.Contains(err.Error(), "node boom") {
		t.Errorf("AddNodes: %v", err)
	}
	if n, _ := s.Nodes(id, NodeQuery{}); len(n) != 0 {
		t.Errorf("failed batch was not rolled back: %v", paths(n))
	}
	if err := s.AddUnmeasured(id, []model.Unmeasured{{Path: "/boom"}}); err == nil || !strings.Contains(err.Error(), "unmeasured boom") {
		t.Errorf("AddUnmeasured: %v", err)
	}
	if err := s.FinishScan(id, model.ScanComplete, 0, 666, t0); err == nil || !strings.Contains(err.Error(), "finish boom") {
		t.Errorf("FinishScan: %v", err)
	}
	if info, _ := s.Scan(id); info.Status != model.ScanRunning {
		t.Errorf("status after failed finish = %s", info.Status)
	}

	mustFinish(t, s, id, model.ScanComplete, t0)
	exec(`CREATE TRIGGER fail_delete BEFORE DELETE ON scans
		BEGIN SELECT RAISE(ABORT, 'delete boom'); END`)
	if err := s.PruneScans(0); err == nil || !strings.Contains(err.Error(), "delete boom") {
		t.Errorf("PruneScans: %v", err)
	}
	exec(`DROP TRIGGER fail_delete`)

	// Malformed rows written behind the store's back fail to scan.
	exec(`INSERT INTO scans (id, root, started_at, status, macdoc_version)
		VALUES ('S-BAD00000', '/', 'not-a-time', 'complete', 'v')`)
	exec(`INSERT INTO nodes (scan_id, path, path_fold, kind, bytes, files, mtime_max, depth, rel_depth)
		VALUES ('S-BAD00000', '/bad', '/bad', 'dir', 'many', 0, 0, 1, 1)`)
	if _, err := s.Scan("S-BAD00000"); err == nil || errors.Is(err, ErrNoScan) {
		t.Errorf("Scan of malformed row: %v", err)
	}
	if _, err := s.LatestScan(model.ScanComplete); err == nil {
		t.Error("LatestScan of malformed row succeeded")
	}
	if _, err := s.Nodes("S-BAD00000", NodeQuery{}); err == nil {
		t.Error("Nodes of malformed row succeeded")
	}
	if _, err := s.History("/bad", 0); err == nil {
		t.Error("History of malformed row succeeded")
	}
}

func BenchmarkAddNodes(b *testing.B) {
	const total, batch = 50_000, 1_000
	nodes := make([]model.Node, total)
	for i := range nodes {
		nodes[i] = dir(fmt.Sprintf("/Users/u/proj%03d/dir%05d", i%500, i), int64(i))
	}
	s, err := Open(filepath.Join(b.TempDir(), "state.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		id, err := s.BeginScan("/", "bench", t0)
		if err != nil {
			b.Fatal(err)
		}
		for i := 0; i < total; i += batch {
			if err := s.AddNodes(id, nodes[i:i+batch]); err != nil {
				b.Fatal(err)
			}
		}
	}
}
