package tests

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
)

// These tests cover nodes.private_bytes (migration 5, ADR 0005 stage 1):
// NULL is unknown and is never read back as 0.

func size(v int64) *int64 { return &v }

func TestPrivateRoundTripKeepsNullAndZeroApart(t *testing.T) {
	s := openTemp(t)
	id := mustBegin(t, s, t0)
	known, zero, unknown := dir("/a", 100), dir("/b", 100), dir("/c", 100)
	known.Private, zero.Private = size(60), size(0)
	mustAdd(t, s, id, known, zero, unknown)
	mustFinish(t, s, id, model.ScanComplete, t0)

	ns, err := s.Nodes(id, store.NodeQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*int64{}
	for _, n := range ns {
		got[n.Path] = n.Private
	}
	if p := got["/a"]; p == nil || *p != 60 {
		t.Errorf("/a private %v; want 60", p)
	}
	if p := got["/b"]; p == nil || *p != 0 {
		t.Errorf("/b private %v; want a known 0", p)
	}
	if p := got["/c"]; p != nil {
		t.Errorf("/c private %d; an unknown size must come back unknown, not 0", *p)
	}
	var nulls int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM nodes WHERE scan_id = ? AND private_bytes IS NULL`, id).Scan(&nulls); err != nil || nulls != 1 {
		t.Errorf("rows with NULL private_bytes = %d, %v; want 1 (only the unknown node)", nulls, err)
	}
}

func TestPrivateIsReplacedOnConflict(t *testing.T) {
	s := openTemp(t)
	id := mustBegin(t, s, t0)
	n := dir("/a", 100)
	n.Private = size(40)
	mustAdd(t, s, id, n)
	n.Private = nil // the same node written again, now unknown
	mustAdd(t, s, id, n)
	ns, err := s.Nodes(id, store.NodeQuery{})
	if err != nil || len(ns) != 1 {
		t.Fatalf("nodes %v, %v", ns, err)
	}
	if ns[0].Private != nil {
		t.Errorf("private %d after rewriting as unknown; the old value must not survive", *ns[0].Private)
	}
}

// writeOldDatabase creates a database at path with only the given earlier
// migrations (store.Migrations is left set to them), stores two nodes in raw
// SQL as the previous release did, and returns their scan ID.
func writeOldDatabase(t *testing.T, path string, old []string) string {
	t.Helper()
	store.Migrations = slices.Clone(old)
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := mustBegin(t, s, t0)
	if _, err := s.DB.Exec(`INSERT INTO nodes
		(scan_id, path, path_fold, kind, bytes, files, mtime_max, depth, rel_depth)
		VALUES (?, '/Users/x', '/users/x', 'dir', 500, 3, 7, 2, 2), (?, '/Users/x/big', '/users/x/big', 'file', 400, 1, 7, 3, 3)`, id, id); err != nil {
		t.Fatalf("old schema insert: %v", err)
	}
	if _, err := s.DB.Exec(`SELECT private_bytes FROM nodes`); err == nil {
		t.Fatal("fixture is wrong: the old schema already has private_bytes")
	}
	mustFinish(t, s, id, model.ScanComplete, t0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}

// A database written by the previous release opens with the new code: its
// rows survive and their private size is unknown, not zero.
func TestPrivateUpgradeKeepsOldRowsUnknown(t *testing.T) {
	saved := store.Migrations
	t.Cleanup(func() { store.Migrations = saved })
	path := filepath.Join(t.TempDir(), "state.db")
	id := writeOldDatabase(t, path, saved[:4]) // the schema before private_bytes (migration 5)

	store.Migrations = saved
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open with the new schema: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var version int
	if err := s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != len(saved) {
		t.Fatalf("schema version %d, %v; want %d", version, err, len(saved))
	}
	ns, err := s.Nodes(id, store.NodeQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 2 || ns[0].Path != "/Users/x" || ns[0].Bytes != 500 || ns[1].Bytes != 400 {
		t.Fatalf("old rows after upgrade: %+v", ns)
	}
	for _, n := range ns {
		if n.Private != nil {
			t.Errorf("%s: private %d after upgrade; rows from before the column are unknown", n.Path, *n.Private)
		}
	}
	// New scans in the same database carry the column.
	id2 := mustBegin(t, s, t0.Add(1))
	n := dir("/new", 10)
	n.Private = size(10)
	mustAdd(t, s, id2, n)
	got, err := s.Nodes(id2, store.NodeQuery{})
	if err != nil || len(got) != 1 || got[0].Private == nil || *got[0].Private != 10 {
		t.Errorf("new scan after upgrade: %+v, %v", got, err)
	}
}

// Reading one more column must not change how a prefix is searched: it is a
// range on the primary key, not a walk of the (scan_id, bytes) index
// (docs/experiments/S4-node-prefix-query). The statement below is the one
// store.Nodes runs for a prefix.
func TestPrivateColumnKeepsThePrefixQueryOnThePrimaryKey(t *testing.T) {
	s := openTemp(t)
	rows, err := s.DB.Query(`EXPLAIN QUERY PLAN
		SELECT path, kind, bytes, files, mtime_max, rel_depth, private_bytes FROM nodes
		WHERE scan_id = ? AND +bytes >= ?
			AND (path_fold = ? OR (path_fold >= ? AND path_fold < ?))
			AND (? < 0 OR depth <= ?)
		ORDER BY bytes DESC, path ASC LIMIT ?`,
		"S-x", 0, "/a", "/a/", "/a0", -1, -1, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "sqlite_autoindex_nodes_1") || strings.Contains(plan, "nodes_scan_bytes") {
		t.Errorf("prefix query plan:\n%s\nwant the primary key and not the bytes index", plan)
	}
}
