package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/sandbox"
	"github.com/sagnik26/macdoc/internal/store"
)

// heldItem applies a one-folder plan and returns the quarantine ID, the entry
// folder and the original path.
func heldItem(t *testing.T, e *env, rel string) (qid, entry, target string) {
	t.Helper()
	target = e.folder(t, rel, "a.log", "sub/b.log")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	rep := mustApply(t, e, p)
	if rep.Items[0].Outcome != executor.OutcomeDone {
		t.Fatalf("setup apply = %+v", rep.Items[0])
	}
	qid = rep.Items[0].QuarantineID
	entry = filepath.Join(quarantine.Root(e.storePath), qid)
	return qid, entry, target
}

func purgeOne(t *testing.T, e *env, id string) executor.EntryResult {
	t.Helper()
	res, err := executor.Purge(context.Background(), e.cfg, []string{id})
	if err != nil || len(res) != 1 {
		t.Fatalf("Purge(%s) = %+v, %v", id, res, err)
	}
	return res[0]
}

func recordStatus(t *testing.T, e *env, id string) store.QuarantineStatus {
	t.Helper()
	rec, err := e.st.Quarantine(id)
	if err != nil {
		t.Fatal(err)
	}
	return rec.Status
}

// Purge refuses to start when the sandbox is missing or the state folder is
// outside it, and then nothing is deleted or recorded.
func TestPurgeRefusesOutsideSandbox(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T){
		"empty":   func(t *testing.T) { t.Setenv(sandbox.EnvVar, "") },
		"another": func(t *testing.T) { t.Setenv(sandbox.EnvVar, t.TempDir()) },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			qid, entry, _ := heldItem(t, e, "Library/Logs/A")
			before := len(e.events(t))
			setup(t)
			res, err := executor.Purge(context.Background(), e.cfg, []string{qid})
			if err == nil || res != nil {
				t.Fatalf("Purge = %+v, %v; want an error and no results", res, err)
			}
			if name == "another" && (!strings.Contains(err.Error(), "nothing was changed") || !strings.Contains(err.Error(), "sandbox")) {
				t.Errorf("refusal = %v", err)
			}
			if !exists(filepath.Join(entry, "payload", "A", "a.log")) || recordStatus(t, e, qid) != store.QuarantineDone {
				t.Fatal("an entry was changed by a refused purge")
			}
			if n := len(e.events(t)); n != before {
				t.Errorf("a refused purge wrote %d audit records", n-before)
			}
		})
	}
}

func TestPurgeNeedsAStore(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	for name, mutate := range map[string]func(c *executor.Config){
		"no store": func(c *executor.Config) { c.Store = nil },
		"no path":  func(c *executor.Config) { c.StorePath = "" },
		"no home":  func(c *executor.Config) { c.Home = "" },
	} {
		cfg := e.cfg
		mutate(&cfg)
		if res, err := executor.Purge(context.Background(), cfg, []string{qid}); err == nil || res != nil {
			t.Errorf("%s: Purge = %+v, %v", name, res, err)
		}
	}
	if !exists(filepath.Join(entry, "payload", "A", "a.log")) {
		t.Fatal("entry deleted by a refused purge")
	}
}

// Each id gets its own result, in order, with the reason it was not touched.
func TestPurgeReportsEachID(t *testing.T) { //nolint:gocyclo // A linear list of assertions about one scenario.
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	res, err := executor.Purge(context.Background(), e.cfg, []string{"nope", "Q-ZZZZ", qid})
	if err != nil || len(res) != 3 {
		t.Fatalf("Purge = %+v, %v", res, err)
	}
	if res[0].ID != "nope" || res[0].Outcome != executor.OutcomeSkipped || res[0].Reason != "not a quarantine ID" {
		t.Errorf("bad id = %+v", res[0])
	}
	if res[1].ID != "Q-ZZZZ" || res[1].Outcome != executor.OutcomeSkipped || !strings.Contains(res[1].Reason, "no record") {
		t.Errorf("unknown id = %+v", res[1])
	}
	if res[2].ID != qid || res[2].Outcome != executor.OutcomeDone || res[2].Original == "" || res[2].Bytes <= 0 {
		t.Errorf("held id = %+v", res[2])
	}
	if exists(entry) || recordStatus(t, e, qid) != store.QuarantinePurged {
		t.Error("the held entry wasn't purged")
	}
	if res, err := executor.Purge(context.Background(), e.cfg, nil); err != nil || len(res) != 0 {
		t.Errorf("Purge(nil) = %+v, %v", res, err)
	}
}

// An entry already purged or restored is not touched again.
func TestPurgeSkipsFinishedEntries(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	if r := purgeOne(t, e, qid); r.Outcome != executor.OutcomeDone {
		t.Fatalf("first purge = %+v", r)
	}
	again := purgeOne(t, e, qid)
	if again.Outcome != executor.OutcomeSkipped || again.Reason != "it was already purged" {
		t.Errorf("second purge = %+v", again)
	}

	e2 := newEnv(t)
	q2, entry2, target2 := heldItem(t, e2, "Library/Logs/B")
	if _, err := executor.Restore(context.Background(), e2.cfg, q2); err != nil {
		t.Fatal(err)
	}
	r := purgeOne(t, e2, q2)
	if r.Outcome != executor.OutcomeSkipped || r.Reason != "it was already restored" {
		t.Errorf("purge of a restored entry = %+v", r)
	}
	if !exists(filepath.Join(target2, "sub", "b.log")) {
		t.Fatal("the restored folder was removed")
	}
	_, _ = entry, entry2
}

// The record is closed, and the leftover manifest tidied, when the moved
// folder is already gone; and a pending record whose folder is gone is left.
func TestPurgeWhenTheFolderIsGone(t *testing.T) { //nolint:gocyclo // A linear list of assertions about one scenario.
	t.Run("payload gone", func(t *testing.T) {
		e := newEnv(t)
		qid, entry, _ := heldItem(t, e, "Library/Logs/A")
		if err := os.RemoveAll(filepath.Join(entry, "payload")); err != nil {
			t.Fatal(err)
		}
		r := purgeOne(t, e, qid)
		if r.Outcome != executor.OutcomeDone || r.Bytes != 0 || r.Reason != "its folder was already gone" {
			t.Fatalf("purge = %+v", r)
		}
		if exists(entry) || recordStatus(t, e, qid) != store.QuarantinePurged {
			t.Error("leftover manifest or record not cleaned up")
		}
	})
	t.Run("entry gone", func(t *testing.T) {
		e := newEnv(t)
		qid, entry, _ := heldItem(t, e, "Library/Logs/A")
		if err := os.RemoveAll(entry); err != nil {
			t.Fatal(err)
		}
		r := purgeOne(t, e, qid)
		if r.Outcome != executor.OutcomeDone || r.Bytes != 0 || recordStatus(t, e, qid) != store.QuarantinePurged {
			t.Fatalf("purge = %+v", r)
		}
	})
	t.Run("pending record", func(t *testing.T) {
		e := newEnv(t)
		target := e.folder(t, "Library/Logs/A")
		id := "Q-PEND"
		now := e.cfg.Now()
		if err := e.st.AddQuarantine(store.QuarantineRecord{ID: id, PlanID: "P-x", ItemID: "O-aaaaaa", RecipeID: "user.logs",
			Original: target, Bytes: 10, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		r := purgeOne(t, e, id)
		if r.Outcome != executor.OutcomeSkipped || r.Reason != "its folder is gone" || r.Original != target || r.Bytes != 10 {
			t.Fatalf("purge = %+v", r)
		}
		if recordStatus(t, e, id) != store.QuarantinePending || !exists(filepath.Join(target, "a.log")) {
			t.Error("a skipped purge changed something")
		}
	})
}

// A damaged entry is refused, with the reason, and kept.
func TestPurgeDamagedReason(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	if err := os.WriteFile(filepath.Join(entry, quarantine.ManifestName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := purgeOne(t, e, qid)
	if r.Outcome != executor.OutcomeSkipped || !strings.HasSuffix(r.Reason, "; it isn't touched") || !strings.HasPrefix(r.Reason, "it looks damaged (") || !strings.Contains(r.Reason, "manifest") {
		t.Fatalf("purge = %+v", r)
	}
	if !exists(filepath.Join(entry, "payload", "A", "a.log")) || recordStatus(t, e, qid) != store.QuarantineDone {
		t.Fatal("a damaged entry was changed")
	}
}

// A state folder reached through a symlink is refused before anything is
// deleted.
func TestPurgeRefusesSymlinkedPath(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	root, _, err := sandbox.Root()
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, filepath.Base(e.home)+"-alias")
	mustSymlink(t, e.home, alias)
	t.Cleanup(func() { _ = os.Remove(alias) })
	cfg := e.cfg
	cfg.StorePath = filepath.Join(alias, strings.TrimPrefix(e.storePath, e.home+"/"))
	res, err := executor.Purge(context.Background(), cfg, []string{qid})
	if err == nil || res != nil || !strings.Contains(err.Error(), "nothing was changed") || !strings.Contains(err.Error(), "non-canonical") {
		t.Fatalf("Purge = %+v, %v", res, err)
	}
	if !exists(filepath.Join(entry, "payload", "A", "a.log")) || recordStatus(t, e, qid) != store.QuarantineDone {
		t.Fatal("the entry was changed")
	}
}

// With the audit log unwritable nothing is deleted.
func TestPurgeNeedsAWritableAuditLog(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	log := audit.Path(e.storePath)
	if err := os.Chmod(log, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(log, 0o600) })
	r := purgeOne(t, e, qid)
	if r.Outcome != executor.OutcomeSkipped || !strings.Contains(r.Reason, "audit log can't be written, so nothing was changed") {
		t.Fatalf("purge = %+v", r)
	}
	if !exists(filepath.Join(entry, "payload", "A", "a.log")) || recordStatus(t, e, qid) != store.QuarantineDone {
		t.Fatal("an entry was deleted without an audit trail")
	}
}

// The payload goes first and the manifest last, so a crash part-way still
// leaves something macdoc can account for.
func TestPurgeRemovesPayloadBeforeManifest(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	seen := false
	e.cfg.StageHook = func(stage string) {
		if stage != "purged-payload" {
			return
		}
		seen = true
		if exists(filepath.Join(entry, "payload")) {
			t.Error("payload still there at purged-payload")
		}
		if !exists(filepath.Join(entry, quarantine.ManifestName)) {
			t.Error("manifest already gone at purged-payload")
		}
	}
	if r := purgeOne(t, e, qid); r.Outcome != executor.OutcomeDone {
		t.Fatalf("purge = %+v", r)
	}
	if !seen {
		t.Error("the purged-payload stage never ran")
	}
	if exists(entry) {
		t.Error("entry folder left behind")
	}
}

// If the audit log fails after the deletion, the purge stands and says the
// record is missing.
func TestPurgeReportsAnAuditFailureAfterDeleting(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	log := audit.Path(e.storePath)
	e.cfg.StageHook = func(stage string) {
		if stage == "purged-payload" {
			_ = os.Chmod(log, 0o400)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(log, 0o600) })
	r := purgeOne(t, e, qid)
	if r.Outcome != executor.OutcomeDone || !strings.HasPrefix(r.Reason, "the audit record couldn't be written: ") {
		t.Fatalf("purge = %+v", r)
	}
	if exists(entry) || recordStatus(t, e, qid) != store.QuarantinePurged {
		t.Error("the purge didn't complete")
	}
}

// What a purge writes to the audit log.
func TestPurgeAuditRecords(t *testing.T) {
	e := newEnv(t)
	qid, _, target := heldItem(t, e, "Library/Logs/A")
	rec, _ := e.st.Quarantine(qid)
	purgeOne(t, e, qid)
	purgeOne(t, e, qid) // refused: already purged
	ev := e.events(t)
	done, skipped := ev[len(ev)-2], ev[len(ev)-1]
	if done.Event != audit.QuarantinePurged || done.QuarantineID != qid || done.Target != target || done.Result != "ok" ||
		done.ActualBytes == nil || *done.ActualBytes != rec.Bytes || done.Reason != "" {
		t.Errorf("purge record = %+v", done)
	}
	if skipped.Event != audit.QuarantinePurged || skipped.QuarantineID != qid || skipped.Result != string(executor.OutcomeSkipped) ||
		skipped.Reason != "it was already purged" || skipped.ActualBytes != nil || skipped.Target != target {
		t.Errorf("refused record = %+v", skipped)
	}
}

// A DeleteNow purge that is refused leaves the item restorable in quarantine
// and says so.
func TestDeleteNowKeepsWhatItCannotPurge(t *testing.T) {
	e := newEnv(t)
	e.cfg.DeleteNow = true
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	log := audit.Path(e.storePath)
	e.cfg.OnItem = func(ev executor.ItemEvent) {
		if ev.Result == nil {
			_ = os.Chmod(log, 0o400)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(log, 0o600) })
	rep := mustApply(t, e, p)
	_ = os.Chmod(log, 0o600)
	it := rep.Items[0]
	if it.Outcome != executor.OutcomeDone || it.Deleted || it.QuarantineID == "" {
		t.Fatalf("item = %+v", it)
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "wasn't deleted") && strings.Contains(w, "it is kept in quarantine as "+it.QuarantineID) {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v", rep.Warnings)
	}
	if !exists(filepath.Join(quarantine.Root(e.storePath), it.QuarantineID, "payload", "A", "a.log")) || exists(target) {
		t.Fatal("the item isn't held in quarantine")
	}
	if rep.DeletedBytes() != 0 || rep.MovedBytes() != it.MovedBytes {
		t.Errorf("deleted %d, held %d", rep.DeletedBytes(), rep.MovedBytes())
	}
	if _, err := executor.Restore(context.Background(), e.cfg, it.QuarantineID); err != nil {
		t.Errorf("the kept item isn't restorable: %v", err)
	}
}

func TestLockQuarantineIsExclusive(t *testing.T) {
	dir := filepath.Join(newEnv(t).home, "state", "macdoc")
	unlock, err := executor.LockQuarantine(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("state folder = %v, %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "quarantine.lock")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("lock file = %v, %v", fi, err)
	}
	var got atomic.Bool
	done := make(chan struct{})
	go func() {
		u, err := executor.LockQuarantine(dir)
		if err == nil {
			got.Store(true)
			u()
		}
		close(done)
	}()
	time.Sleep(300 * time.Millisecond)
	if got.Load() {
		t.Fatal("a second lock was taken while the first was held")
	}
	unlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the second lock never got in after unlock")
	}
	if !got.Load() {
		t.Fatal("the second lock failed")
	}
}

func TestLockQuarantineFailsWithoutAFolder(t *testing.T) {
	home := newEnv(t).home
	file := filepath.Join(home, "plainfile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if u, err := executor.LockQuarantine(filepath.Join(file, "sub")); err == nil {
		u()
		t.Error("lock under a file succeeded")
	}
	ro := filepath.Join(home, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) }) //nolint:gosec // G302: lets the cleanup remove it.
	if u, err := executor.LockQuarantine(ro); err == nil {
		u()
		t.Error("lock in a read-only folder succeeded")
	}
}
