package tests

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/quarantine"
)

// A swap for a symlink while the move is being prepared is named as such, and
// what the link points at is left alone.
func TestSwapForSymlinkIsNamed(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	victim := e.folder(t, "Documents/Thesis", "chapter1.md")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	e.cfg.StageHook = func(stage string) {
		if stage == "pending" {
			mustRename(t, target, target+".moved")
			mustSymlink(t, victim, target)
		}
	}
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeSkipped || it.Reason != "it is now a symlink" || it.QuarantineID != "" {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(victim, "chapter1.md")) || !exists(filepath.Join(target+".moved", "a.log")) {
		t.Fatal("something was moved")
	}
}

// A different folder swapped in at the same path is a different folder.
func TestSwapForAnotherFolderIsNamed(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	e.cfg.StageHook = func(stage string) {
		if stage == "pending" {
			mustRename(t, target, target+".moved")
			e.folder(t, "Library/Logs/A", "other.txt")
		}
	}
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeSkipped || it.Reason != "it was replaced by something else" {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(target, "other.txt")) || !exists(filepath.Join(target+".moved", "a.log")) {
		t.Fatal("something was moved")
	}
}

// The folder holding the target turning into a file mid-move is refused.
func TestSwapParentForFile(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	parent := filepath.Dir(target)
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	e.cfg.StageHook = func(stage string) {
		if stage == "pending" {
			mustRename(t, parent, parent+".moved")
			if err := os.WriteFile(parent, []byte("now a file"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeSkipped || !strings.Contains(it.Reason, "its folder can't be opened") {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(parent+".moved", "A", "a.log")) {
		t.Fatal("the folder was lost")
	}
	if l, _ := quarantine.List(e.st, quarantine.Root(e.storePath), e.cfg.Now()); len(l.Entries) != 0 || len(l.Unknown) != 0 {
		t.Errorf("canceled entry left behind: %+v", l)
	}
}

// makeUnreadable makes dir unreadable and registers cleanup that makes every
// folder under home readable again.
func makeUnreadable(t *testing.T, home, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, _ error) error {
			if d != nil && d.IsDir() {
				_ = os.Chmod(p, 0o700) //nolint:gosec // G302: lets the cleanup remove it.
			}
			return nil
		})
	})
}

// A folder with a part that can't be read can't be checked, so it stays.
func TestUnreadablePartSkips(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "a.log", "locked/x.log")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	makeUnreadable(t, e.home, filepath.Join(target, "locked"))
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeSkipped || !strings.Contains(it.Reason, "can't be read, so it can't be checked") || it.QuarantineID != "" {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(target, "a.log")) {
		t.Fatal("the folder was moved")
	}
}

// Vendored package trees are not read at all, so one with an unreadable part
// does not stop the move.
func TestVendoredTreeIsNotRead(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "a.log", "node_modules/pkg/index.js")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	makeUnreadable(t, e.home, filepath.Join(target, "node_modules", "pkg"))
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeDone {
		t.Fatalf("item = %+v", it)
	}
}

// A target that can't be looked at (not just a missing one) is not moved.
func TestUnreadableTargetIsNotMoved(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	parent := filepath.Dir(target)
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	makeUnreadable(t, e.home, parent)
	rep, err := executor.Apply(context.Background(), e.cfg, p, all(p))
	_ = os.Chmod(parent, 0o755) //nolint:gosec // G302: restore the fixture.
	if err != nil {
		t.Fatal(err)
	}
	if it := rep.Items[0]; it.Outcome != executor.OutcomeSkipped || !strings.HasPrefix(it.Reason, "it can't be read: ") || it.QuarantineID != "" {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(target, "a.log")) {
		t.Fatal("moved")
	}
}
