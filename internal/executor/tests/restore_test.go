package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
)

// Restore puts a folder back, so each way it can be refused must leave the
// quarantined copy exactly where it was. Mutation testing
// showed that none of these branches was asserted: deleting a refusal left
// every test green.

// quarantined applies a one-folder plan and returns the folder's path, its
// quarantine ID and where the moved folder now sits.
func quarantined(t *testing.T, e *env, rel string) (target, qid, payload string) {
	t.Helper()
	target = e.folder(t, rel)
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	qid = mustApply(t, e, p).Items[0].QuarantineID
	if qid == "" {
		t.Fatal("the item was not quarantined")
	}
	payload = filepath.Join(quarantine.Root(e.storePath), qid, "payload", filepath.Base(target))
	if !exists(payload) || exists(target) {
		t.Fatalf("fixture: payload %v target %v", exists(payload), exists(target))
	}
	return target, qid, payload
}

// refused asserts Restore failed with ErrNotRestorable (or any error when
// wantNotRestorable is false) naming why, and that nothing was moved.
func refused(t *testing.T, e *env, qid, target, payload, why string, wantNotRestorable bool) {
	t.Helper()
	res, err := executor.Restore(context.Background(), e.cfg, qid)
	if err == nil {
		t.Fatalf("Restore succeeded, want a refusal containing %q", why)
	}
	if wantNotRestorable && !errors.Is(err, executor.ErrNotRestorable) {
		t.Errorf("err = %v, want ErrNotRestorable", err)
	}
	if !strings.Contains(err.Error(), why) {
		t.Errorf("err = %q, want it to contain %q", err, why)
	}
	if res.Outcome == executor.OutcomeDone {
		t.Errorf("result says done: %+v", res)
	}
	if !exists(payload) {
		t.Error("the quarantined folder is gone after a refused restore")
	}
	if exists(target) {
		t.Error("something was put back by a refused restore")
	}
}

func TestRestorePutsTheFolderBack(t *testing.T) {
	e := newEnv(t)
	target, qid, payload := quarantined(t, e, "Library/Logs/A")

	res, err := executor.Restore(context.Background(), e.cfg, qid)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != executor.OutcomeDone || res.Original != target || res.Bytes <= 0 {
		t.Errorf("result = %+v", res)
	}
	if !exists(filepath.Join(target, "a.log")) || exists(payload) {
		t.Error("the folder was not moved back")
	}
	if rec, err := e.st.Quarantine(qid); err != nil || rec.Status != store.QuarantineRestored {
		t.Errorf("record = %+v, %v; want status restored", rec, err)
	}
	// A second restore has nothing to restore.
	if _, err := executor.Restore(context.Background(), e.cfg, qid); err == nil {
		t.Error("restoring twice succeeded")
	}
}

func TestRestoreRefusals(t *testing.T) {
	t.Run("not a quarantine ID", func(t *testing.T) {
		e := newEnv(t)
		if _, err := executor.Restore(context.Background(), e.cfg, "P-ABCD"); err == nil || !strings.Contains(err.Error(), "isn't a quarantine ID") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no such entry", func(t *testing.T) {
		e := newEnv(t)
		if _, err := executor.Restore(context.Background(), e.cfg, "Q-ZZZZ"); err == nil {
			t.Error("an entry macdoc never made was restored")
		}
	})
	t.Run("the path to the original changed", func(t *testing.T) {
		e := newEnv(t)
		target, qid, payload := quarantined(t, e, "Library/Logs/A")
		// Logs becomes a symlink: the original now resolves somewhere else.
		// (Swapping Library instead would trip the state-directory guard first,
		// because macdoc's own state lives under it.)
		logs := filepath.Dir(target)
		moved := filepath.Join(e.home, "LogsReal")
		if err := os.Rename(logs, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, logs); err != nil {
			t.Fatal(err)
		}
		// Two independent layers refuse this: the sandbox check, which runs
		// first under MACDOC_SANDBOX (as in tests), and Restore's own
		// canonical-path check. Either way nothing may be moved.
		refused(t, e, qid, target, payload, "non-canonical", false)
	})
	t.Run("the folder it came from is gone", func(t *testing.T) {
		e := newEnv(t)
		target, qid, payload := quarantined(t, e, "Library/Logs/A")
		if err := os.RemoveAll(filepath.Dir(target)); err != nil {
			t.Fatal(err)
		}
		refused(t, e, qid, target, payload, "can't be opened", true)
	})
	t.Run("the recipe left the catalog", func(t *testing.T) {
		e := newEnv(t)
		target, qid, payload := quarantined(t, e, "Library/Logs/A")
		e.cfg.Catalog = &recipes.Catalog{}
		refused(t, e, qid, target, payload, "no longer in the catalog", true)
	})
	t.Run("the quarantined folder was swapped", func(t *testing.T) {
		e := newEnv(t)
		target, qid, payload := quarantined(t, e, "Library/Logs/A")
		if err := os.Rename(payload, payload+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(payload, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := executor.Restore(context.Background(), e.cfg, qid)
		if err == nil {
			t.Fatal("a swapped folder was restored")
		}
		if exists(target) {
			t.Error("the swapped folder was put back")
		}
	})
	t.Run("the audit log cannot be written", func(t *testing.T) {
		e := newEnv(t)
		target, qid, payload := quarantined(t, e, "Library/Logs/A")
		log := audit.Path(e.storePath)
		if err := os.Remove(log); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(log, 0o700); err != nil {
			t.Fatal(err)
		}
		// Nothing may change if the change could not be recorded.
		refused(t, e, qid, target, payload, "nothing was changed", false)
	})
}

// A folder above the original can stop being writable. Restore must fail,
// say so, and leave the quarantined copy where it was.
func TestRestoreFailsWhenTheOriginalFolderCannotBeWritten(t *testing.T) {
	e := newEnv(t)
	target, qid, payload := quarantined(t, e, "Library/Logs/A")
	parent := filepath.Dir(target)
	if err := os.Chmod(parent, 0o500); err != nil { //nolint:gosec // G302: a fixture folder made read-only on purpose.
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) }) //nolint:gosec // G302: lets the temp folder be removed.

	res, err := executor.Restore(context.Background(), e.cfg, qid)
	if err == nil || res.Outcome == executor.OutcomeDone {
		t.Fatalf("Restore = %+v, %v; want a failure", res, err)
	}
	if !exists(payload) || exists(target) {
		t.Errorf("after a failed restore: payload present %v, target present %v", exists(payload), exists(target))
	}
	if rec, rerr := e.st.Quarantine(qid); rerr != nil || rec.Status == store.QuarantineRestored {
		t.Errorf("record = %+v, %v; a failed restore must not be recorded as restored", rec, rerr)
	}
}

// If the record can't be closed after the folder is back, the folder stays
// restored and the error says the record is what failed.
func TestRestoreRecordUpdateFails(t *testing.T) {
	e := newEnv(t)
	target, qid, _ := quarantined(t, e, "Library/Logs/A")
	refuseRecordUpdates(t, e)

	_, err := executor.Restore(context.Background(), e.cfg, qid)
	if err == nil || !strings.Contains(err.Error(), "restored, but macdoc's record couldn't be updated") {
		t.Fatalf("err = %v", err)
	}
	if !exists(filepath.Join(target, "a.log")) {
		t.Error("the folder was not left restored")
	}
}
