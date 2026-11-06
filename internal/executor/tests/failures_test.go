package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/store"
)

// refuseRecordUpdates makes the quarantine table refuse every UPDATE, as a
// store that can't be written would.
func refuseRecordUpdates(t *testing.T, e *env) {
	t.Helper()
	if _, err := e.st.DB.Exec(`CREATE TRIGGER refuse_updates BEFORE UPDATE ON quarantine
		BEGIN SELECT RAISE(ABORT, 'record refused'); END`); err != nil {
		t.Fatal(err)
	}
}

// If the record can't be closed after the delete, the person is told it failed
// (the folder is already gone).
func TestPurgeRecordUpdateFails(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	refuseRecordUpdates(t, e)
	r := purgeOne(t, e, qid)
	if r.Outcome != executor.OutcomeFailed || !strings.HasPrefix(r.Reason, "deleted, but macdoc's record couldn't be updated: ") ||
		!strings.Contains(r.Reason, "record refused") {
		t.Fatalf("purge = %+v", r)
	}
	if exists(entry) {
		t.Error("the entry wasn't deleted")
	}
	if recordStatus(t, e, qid) != store.QuarantineDone {
		t.Error("the record changed despite the refusal")
	}
}

// If the record of an already-empty entry can't be closed, the entry is left
// as it is and the reason says why.
func TestPurgeGoneFolderRecordUpdateFails(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	if err := os.RemoveAll(filepath.Join(entry, "payload")); err != nil {
		t.Fatal(err)
	}
	refuseRecordUpdates(t, e)
	r := purgeOne(t, e, qid)
	if r.Outcome != executor.OutcomeSkipped || !strings.Contains(r.Reason, "record refused") {
		t.Fatalf("purge = %+v", r)
	}
	if !exists(filepath.Join(entry, quarantine.ManifestName)) || recordStatus(t, e, qid) != store.QuarantineDone {
		t.Error("the entry or its record was changed")
	}
}

// Something that can't be deleted fails the purge, says so, and keeps the
// record open.
func TestPurgeFailsWhenAFileCannotBeDeleted(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	stuck := filepath.Join(entry, "payload", "A", "sub", "b.log")
	if err := unix.Chflags(stuck, unix.UF_IMMUTABLE); err != nil {
		t.Skipf("can't set the immutable flag here: %v", err)
	}
	t.Cleanup(func() { _ = unix.Chflags(stuck, 0) })
	r := purgeOne(t, e, qid)
	if r.Outcome != executor.OutcomeFailed || !strings.Contains(r.Reason, "couldn't delete all of it") {
		t.Fatalf("purge = %+v", r)
	}
	if !exists(stuck) || recordStatus(t, e, qid) != store.QuarantineDone {
		t.Error("the stuck file vanished or the record was closed")
	}
}

// The manifest goes last; if it can't be deleted the purge says so, and the
// entry stays accounted for.
func TestPurgeFailsWhenTheManifestCannotBeDeleted(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	manifest := filepath.Join(entry, quarantine.ManifestName)
	if err := unix.Chflags(manifest, unix.UF_IMMUTABLE); err != nil {
		t.Skipf("can't set the immutable flag here: %v", err)
	}
	t.Cleanup(func() { _ = unix.Chflags(manifest, 0) })
	r := purgeOne(t, e, qid)
	if r.Outcome != executor.OutcomeFailed || !strings.HasPrefix(r.Reason, "couldn't delete its manifest: ") {
		t.Fatalf("purge = %+v", r)
	}
	if !exists(manifest) || recordStatus(t, e, qid) != store.QuarantineDone {
		t.Error("the manifest vanished or the record was closed")
	}
}

// An entry whose payload folder is missing altogether can't be vouched for.
func TestPurgeRefusesAnEntryWithoutAPayloadFolder(t *testing.T) {
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
	entry := theEntry(t, e)
	qid := filepath.Base(entry)
	if err := os.Remove(filepath.Join(entry, quarantine.Payload)); err != nil {
		t.Fatal(err)
	}
	r := purgeOne(t, e, qid)
	if r.Outcome != executor.OutcomeSkipped || !strings.HasSuffix(r.Reason, "; it isn't touched") || !strings.Contains(r.Reason, "no such file") {
		t.Fatalf("purge = %+v", r)
	}
	if !exists(filepath.Join(entry, quarantine.ManifestName)) || !exists(filepath.Join(target, "a.log")) {
		t.Fatal("something was deleted")
	}
}

// A payload with a part that can't be looked at can't be checked, so it is
// not deleted.
func TestPurgeRefusesWhatItCannotCheck(t *testing.T) {
	e := newEnv(t)
	qid, entry, _ := heldItem(t, e, "Library/Logs/A")
	locked := filepath.Join(entry, "payload", "A", "sub")
	makeUnreadable(t, e.home, locked)
	r := purgeOne(t, e, qid)
	if r.Outcome != executor.OutcomeSkipped || !strings.Contains(r.Reason, "permission denied") || !strings.HasSuffix(r.Reason, "; it isn't touched") {
		t.Fatalf("purge = %+v", r)
	}
	_ = os.Chmod(locked, 0o700) //nolint:gosec // G302: lets the check below look.
	if !exists(filepath.Join(entry, "payload", "A", "a.log")) || !exists(filepath.Join(locked, "b.log")) || recordStatus(t, e, qid) != store.QuarantineDone {
		t.Fatal("something was deleted")
	}
}
