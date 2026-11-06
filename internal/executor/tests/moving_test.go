package tests

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/store"
)

// allocated is what the executor measures: the allocated bytes of every
// entry in the tree, the folder itself included.
func allocated(t *testing.T, root string) int64 {
	t.Helper()
	var n int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		n += fi.Sys().(*syscall.Stat_t).Blocks * 512
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A SAFE folder that changed since the plan is re-measured over every entry
// in it, and the measured size is what the record and manifest hold.
func TestMovedBytesAreMeasuredOverTheWholeTree(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "a.log", "sub/deeper/b.log")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	for name, size := range map[string]int{"n1.bin": 40000, "sub/n2.bin": 70000, "sub/deeper/n3.bin": 100000} {
		if err := os.WriteFile(filepath.Join(target, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := allocated(t, target)
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeDone || it.MovedBytes != want {
		t.Fatalf("item = %+v, want %d bytes moved", it, want)
	}
	rec, err := e.st.Quarantine(it.QuarantineID)
	if err != nil || rec.Bytes != want {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	m, err := quarantine.LoadManifest(quarantine.Root(e.storePath), it.QuarantineID)
	if err != nil || m.Bytes != want || m.BytesSource != model.SourceMeasured {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
}

// A single file is moved as well. Unchanged, it keeps the planned size; if it
// changed since the plan (SAFE only) it is measured again.
func TestMovedFileBytes(t *testing.T) {
	for _, changed := range []bool{false, true} {
		e := newEnv(t)
		dir := e.folder(t, "Library/Logs")
		target := filepath.Join(dir, "one.log")
		if err := os.WriteFile(target, make([]byte, 3000), 0o644); err != nil {
			t.Fatal(err)
		}
		old := t0.Add(-60 * 24 * time.Hour)
		if err := os.Chtimes(target, old, old); err != nil {
			t.Fatal(err)
		}
		p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
		want := int64(8192)
		if changed {
			if err := os.WriteFile(target, make([]byte, 300000), 0o644); err != nil {
				t.Fatal(err)
			}
			want = allocated(t, target)
		}
		it := mustApply(t, e, p).Items[0]
		if it.Outcome != executor.OutcomeDone || it.MovedBytes != want {
			t.Fatalf("changed=%v: item = %+v, want %d bytes", changed, it, want)
		}
		if exists(target) || !exists(filepath.Join(quarantine.Root(e.storePath), it.QuarantineID, "payload", "one.log")) {
			t.Fatalf("changed=%v: the file wasn't moved into quarantine", changed)
		}
	}
}

// What the manifest and record say about a move.
func TestManifestContents(t *testing.T) { //nolint:gocyclo // A linear list of assertions about one scenario.
	e := newEnv(t)
	e.cfg.QuarantineTTL = 48 * time.Hour
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	it := mustApply(t, e, p).Items[0]
	root := quarantine.Root(e.storePath)
	m, err := quarantine.LoadManifest(root, it.QuarantineID)
	if err != nil {
		t.Fatal(err)
	}
	now := e.cfg.Now()
	if m.Plan != p.ID || m.Item != "O-aaaaaa" || m.Recipe != "user.logs" || m.Original != target || m.Name != "A" ||
		m.Status != quarantine.StatusDone || m.Title != p.Items[0].Title || m.Bytes != 8192 || m.BytesSource != model.SourceMeasured {
		t.Errorf("manifest = %+v", m)
	}
	if !m.CreatedAt.Equal(now) || !m.ExpiresAt.Equal(now.Add(48*time.Hour)) {
		t.Errorf("times = %v, %v; want %v and 48h later", m.CreatedAt, m.ExpiresAt, now)
	}
	if m.Fingerprint != p.Items[0].Fingerprint {
		t.Errorf("fingerprint = %+v, want %+v", m.Fingerprint, p.Items[0].Fingerprint)
	}
	rec, _ := e.st.Quarantine(it.QuarantineID)
	if rec.Status != store.QuarantineDone || rec.PlanID != p.ID || rec.ItemID != "O-aaaaaa" || rec.RecipeID != "user.logs" ||
		rec.Original != target || !rec.ExpiresAt.Equal(m.ExpiresAt) || !rec.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("record = %+v", rec)
	}
	mp := quarantine.ManifestPath(root, it.QuarantineID)
	if fi, err := os.Stat(mp); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("manifest file = %v, %v", fi, err)
	}
	if exists(mp + ".tmp") {
		t.Error("manifest temp file left behind")
	}
	if fi, err := os.Stat(filepath.Join(root, it.QuarantineID)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("entry folder = %v, %v", fi, err)
	}

	// The default lifetime applies when none is set.
	e2 := newEnv(t)
	p2 := e2.plan(t, e2.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e2.folder(t, "Library/Logs/A")))
	it2 := mustApply(t, e2, p2).Items[0]
	m2, err := quarantine.LoadManifest(quarantine.Root(e2.storePath), it2.QuarantineID)
	if err != nil || !m2.ExpiresAt.Equal(e2.cfg.Now().Add(quarantine.DefaultTTL)) {
		t.Errorf("default expiry = %v, %v", m2.ExpiresAt, err)
	}
}

// Entry IDs are well formed and never repeat.
func TestEntryIDs(t *testing.T) {
	e := newEnv(t)
	var items []model.PlanItem
	for i := range 12 {
		id := "O-" + strings.Repeat(string(rune('a'+i)), 6)
		items = append(items, e.item(t, id, "user.logs", model.RiskSafe, e.folder(t, filepath.Join("Library/Logs", "F"+string(rune('a'+i))))))
	}
	rep := mustApply(t, e, e.plan(t, items...))
	var ids []string
	for _, it := range rep.Items {
		if it.Outcome != executor.OutcomeDone || !quarantine.ValidID(it.QuarantineID) {
			t.Fatalf("item = %+v", it)
		}
		ids = append(ids, it.QuarantineID)
	}
	slices.Sort(ids)
	if len(slices.Compact(ids)) != len(ids) {
		t.Fatalf("repeated IDs: %v", ids)
	}
}

// The quarantine folder is made private, excluded from Spotlight, and a
// loose one is tightened.
func TestQuarantineRootIsPrepared(t *testing.T) {
	e := newEnv(t)
	root := quarantine.Root(e.storePath)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil { //nolint:gosec // G302: a loose fixture folder.
		t.Fatal(err)
	}
	mustApply(t, e, e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A"))))
	if fi, err := os.Stat(root); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("quarantine folder = %v, %v", fi, err)
	}
	marker := filepath.Join(root, ".metadata_never_index")
	if fi, err := os.Stat(marker); err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 0 {
		t.Errorf("marker = %v, %v", fi, err)
	}
	if fi, err := os.Stat(e.cfg.StorePath[:strings.LastIndex(e.cfg.StorePath, "/")]); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("state folder = %v, %v", fi, err)
	}
}

// A quarantine folder that can't be prepared fails the item and moves
// nothing.
func TestQuarantineRootBlocked(t *testing.T) {
	e := newEnv(t)
	root := quarantine.Root(e.storePath)
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("a file where the folder belongs"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := e.folder(t, "Library/Logs/A")
	it := mustApply(t, e, e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))).Items[0]
	if it.Outcome != executor.OutcomeFailed || !strings.Contains(it.Reason, "couldn't prepare the quarantine folder") || it.QuarantineID != "" {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(target, "a.log")) {
		t.Fatal("the folder was moved despite the failure")
	}
	if b, _ := os.ReadFile(root); string(b) != "a file where the folder belongs" {
		t.Fatal("the file in the way was changed")
	}
}

// A symlink where the quarantine folder belongs is not followed.
func TestQuarantineRootSymlinkRefused(t *testing.T) {
	e := newEnv(t)
	root := quarantine.Root(e.storePath)
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(e.home, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, elsewhere, root)
	target := e.folder(t, "Library/Logs/A")
	it := mustApply(t, e, e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))).Items[0]
	if it.Outcome == executor.OutcomeDone || !exists(filepath.Join(target, "a.log")) {
		t.Fatalf("item = %+v", it)
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
		t.Fatalf("something was written through the symlink: %v", ents)
	}
}

// A folder named HEAD is not a bare repository's HEAD file, even with
// objects and refs beside it.
func TestHeadFolderIsNotARepository(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "x/HEAD/f", "x/objects/o", "x/refs/r")
	it := mustApply(t, e, e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))).Items[0]
	if it.Outcome != executor.OutcomeDone {
		t.Fatalf("item = %+v", it)
	}
}

// A bare repository whose HEAD is a symlink is still a repository.
func TestSymlinkedHeadIsARepository(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "x/objects/o", "x/refs/r", "x/real")
	mustSymlink(t, filepath.Join(target, "x", "real"), filepath.Join(target, "x", "HEAD"))
	if err := os.Chtimes(target, t0.Add(-60*24*time.Hour), t0.Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	it := mustApply(t, e, e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))).Items[0]
	if it.Outcome != executor.OutcomeSkipped || !strings.Contains(it.Reason, "git repository") || !exists(target) {
		t.Fatalf("item = %+v", it)
	}
}

// Restoring or purging an entry whose move never started says so.
func TestNotMovedEntryExplained(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	e.cfg.StageHook = func(stage string) {
		if stage == "pending" {
			panic("stop here, as a crash would")
		}
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = executor.Apply(context.Background(), e.cfg, p, all(p))
	}()
	e.cfg.StageHook = nil
	l, err := quarantine.List(e.st, quarantine.Root(e.storePath), e.cfg.Now())
	if err != nil || len(l.Entries) != 1 || l.Entries[0].State != quarantine.StateNotMoved {
		t.Fatalf("listing = %+v, %v", l, err)
	}
	qid := l.Entries[0].Record.ID
	_, err = executor.Restore(context.Background(), e.cfg, qid)
	if err == nil || !strings.Contains(err.Error(), "apply stopped before moving it, so the original was never touched") {
		t.Fatalf("restore = %v", err)
	}
	if !exists(filepath.Join(target, "a.log")) {
		t.Fatal("the original was touched")
	}
}
