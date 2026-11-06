package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"macdoc/internal/executor"
	"macdoc/internal/model"
	"macdoc/internal/quarantine"
	"macdoc/internal/store"
)

// theEntry returns the one quarantine entry folder that exists while a move is
// in progress.
func theEntry(t *testing.T, e *env) string {
	t.Helper()
	root := quarantine.Root(e.storePath)
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, d := range ents {
		if quarantine.ValidID(d.Name()) {
			found = filepath.Join(root, d.Name())
		}
	}
	if found == "" {
		t.Fatal("no entry folder yet")
	}
	return found
}

// stageFixture is one SAFE log folder and its plan, with a hook for stage.
type stageFixture struct {
	e              *env
	target, parent string
	plan           model.Plan
}

func newStageFixture(t *testing.T, stage string, act func(f *stageFixture)) *stageFixture {
	t.Helper()
	e := newEnv(t)
	f := &stageFixture{e: e}
	f.target = e.folder(t, "Library/Logs/A", "a.log", "sub/b.log")
	f.parent = filepath.Dir(f.target)
	f.plan = e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, f.target))
	e.cfg.StageHook = func(s string) {
		if s == stage {
			act(f)
		}
	}
	return f
}

func (f *stageFixture) apply(t *testing.T) (executor.Report, executor.ItemResult) {
	t.Helper()
	rep := mustApply(t, f.e, f.plan)
	return rep, rep.Items[0]
}

func (f *stageFixture) noEntriesLeft(t *testing.T) {
	t.Helper()
	l, _ := quarantine.List(f.e.st, quarantine.Root(f.e.storePath), f.e.cfg.Now())
	if len(l.Entries) != 0 || len(l.Unknown) != 0 {
		t.Errorf("canceled entry left behind: %+v", l)
	}
}

// The folder holding the target swapped for a symlink once its entry is
// recorded: refused, and neither the data nor the link's destination moves.
func TestParentSwappedForSymlink(t *testing.T) {
	var victim string
	f := newStageFixture(t, "opened", func(f *stageFixture) {
		mustRename(t, f.parent, f.parent+".moved")
		mustSymlink(t, victim, f.parent)
	})
	victim = f.e.folder(t, "Documents/Elsewhere", "keep.md")
	_, it := f.apply(t)
	if it.Outcome != executor.OutcomeSkipped || !strings.Contains(it.Reason, "its folder changed during the move: ") || it.QuarantineID != "" {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(f.parent+".moved", "A", "a.log")) || !exists(filepath.Join(victim, "keep.md")) {
		t.Fatal("something was moved")
	}
	f.noEntriesLeft(t)
}

// The folder holding the target swapped for another real folder: the one we
// hold is no longer the one at that path, so nothing moves.
func TestParentSwappedForAnotherFolder(t *testing.T) {
	f := newStageFixture(t, "opened", func(f *stageFixture) {
		mustRename(t, f.parent, f.parent+".moved")
		if err := os.Mkdir(f.parent, 0o755); err != nil {
			t.Fatal(err)
		}
	})
	_, it := f.apply(t)
	if it.Outcome != executor.OutcomeSkipped || it.Reason != "its folder changed during the move" {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(f.parent+".moved", "A", "a.log")) {
		t.Fatal("the folder was moved")
	}
	f.noEntriesLeft(t)
}

// A target that vanishes before the move is "can't be found", not "replaced".
func TestTargetVanishesBeforeMove(t *testing.T) {
	f := newStageFixture(t, "pending", func(f *stageFixture) {
		mustRename(t, f.target, f.target+".gone")
	})
	_, it := f.apply(t)
	if it.Outcome != executor.OutcomeSkipped || !strings.HasPrefix(it.Reason, "it can't be found: ") {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(f.target+".gone", "a.log")) {
		t.Fatal("data lost")
	}
	f.noEntriesLeft(t)
}

// A payload folder that turns into a symlink can't be moved into.
func TestPayloadSwappedForSymlink(t *testing.T) {
	var victim string
	f := newStageFixture(t, "opened", func(f *stageFixture) {
		payload := filepath.Join(theEntry(t, f.e), quarantine.PayloadDir)
		if err := os.Remove(payload); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, victim, payload)
	})
	victim = f.e.folder(t, "Documents/Elsewhere", "keep.md")
	_, it := f.apply(t)
	if it.Outcome != executor.OutcomeFailed || strings.Contains(it.Reason, "the move failed") || it.QuarantineID != "" {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(f.target, "a.log")) || !exists(filepath.Join(victim, "keep.md")) {
		t.Fatal("something was moved")
	}
	if ents, _ := os.ReadDir(victim); len(ents) != 1 {
		t.Fatalf("the symlink's destination was written to: %v", ents)
	}
}

// A name already taken inside the payload is never overwritten.
func TestMoveNeverOverwritesInThePayload(t *testing.T) {
	f := newStageFixture(t, "opened", func(f *stageFixture) {
		squat := filepath.Join(theEntry(t, f.e), quarantine.PayloadDir, "A")
		if err := os.MkdirAll(squat, 0o700); err != nil {
			t.Fatal(err)
		}
	})
	_, it := f.apply(t)
	if it.Outcome != executor.OutcomeFailed || !strings.HasPrefix(it.Reason, "the move failed: ") {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(f.target, "sub", "b.log")) {
		t.Fatal("the folder was moved")
	}
}

// The parent swapped after the move: the folder goes back where it was.
func TestParentSwappedAfterMoveIsUndone(t *testing.T) {
	f := newStageFixture(t, "moved", func(f *stageFixture) {
		mustRename(t, f.parent, f.parent+".moved")
		if err := os.Mkdir(f.parent, 0o755); err != nil {
			t.Fatal(err)
		}
	})
	_, it := f.apply(t)
	if it.Outcome != executor.OutcomeFailed || it.Reason != "its folder changed during the move; it was moved back" {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(f.parent+".moved", "A", "sub", "b.log")) {
		t.Fatal("the folder wasn't put back where it came from")
	}
	f.noEntriesLeft(t)
}

// A move that can't be undone stays in quarantine and says where.
func TestFailedMoveBackStaysInQuarantine(t *testing.T) {
	f := newStageFixture(t, "moved", func(f *stageFixture) {
		payload := filepath.Join(theEntry(t, f.e), quarantine.PayloadDir, "A")
		mustRename(t, payload, payload+".real")
		if err := os.Mkdir(payload, 0o700); err != nil {
			t.Fatal(err)
		}
		// Something already sits where the folder would go back to.
		f.e.folder(t, "Library/Logs/A", "newcomer.txt")
	})
	_, it := f.apply(t)
	if it.Outcome != executor.OutcomeFailed || it.QuarantineID == "" ||
		!strings.Contains(it.Reason, "it was replaced by something else; it couldn't be moved back and stays in quarantine as "+it.QuarantineID) {
		t.Fatalf("item = %+v", it)
	}
	entry := filepath.Join(quarantine.Root(f.e.storePath), it.QuarantineID)
	if !exists(filepath.Join(entry, "payload", "A.real", "a.log")) || !exists(filepath.Join(f.target, "newcomer.txt")) {
		t.Fatal("data lost or the newcomer disturbed")
	}
}

// A record that can't be updated after the move is a warning: the item is
// moved and stays restorable.
func TestRecordUpdateFailureWarns(t *testing.T) {
	f := newStageFixture(t, "done", func(f *stageFixture) { _ = f.e.st.Close() })
	rep, it := f.apply(t)
	if it.Outcome != executor.OutcomeDone || it.QuarantineID == "" || exists(f.target) {
		t.Fatalf("item = %+v", it)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], it.QuarantineID+": moved, but macdoc's record couldn't be updated") ||
		!strings.Contains(rep.Warnings[0], "still restorable") {
		t.Fatalf("warnings = %v", rep.Warnings)
	}
	st, err := store.Open(f.e.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	l, err := quarantine.List(st, quarantine.Root(f.e.storePath), f.e.cfg.Now())
	if err != nil || len(l.Entries) != 1 || !l.Entries[0].Restorable() {
		t.Fatalf("listing = %+v, %v", l, err)
	}
}

// A manifest that can't be updated after the move is a warning too.
func TestManifestUpdateFailureWarns(t *testing.T) {
	f := newStageFixture(t, "moved", func(f *stageFixture) {
		if err := os.MkdirAll(filepath.Join(theEntry(t, f.e), quarantine.ManifestName+".tmp", "x"), 0o700); err != nil {
			t.Fatal(err)
		}
	})
	rep, it := f.apply(t)
	if it.Outcome != executor.OutcomeDone || it.QuarantineID == "" || exists(f.target) {
		t.Fatalf("item = %+v", it)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], it.QuarantineID+": moved, but its manifest couldn't be updated") ||
		!strings.Contains(rep.Warnings[0], "still restorable") {
		t.Fatalf("warnings = %v", rep.Warnings)
	}
	l, err := quarantine.List(f.e.st, quarantine.Root(f.e.storePath), f.e.cfg.Now())
	if err != nil || len(l.Entries) != 1 || !l.Entries[0].Restorable() {
		t.Fatalf("listing = %+v, %v", l, err)
	}
}

// A CAUTION folder that has not changed since the plan is moved; only a
// changed one is left for a new plan.
func TestUnchangedCautionFolderMoves(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Caches/com.example.app")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.app_caches", model.RiskCaution, target))
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeDone || exists(target) {
		t.Fatalf("item = %+v", it)
	}
}

// Look-alikes of repositories and environment files are not refused.
func TestContentLookAlikes(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		skip  string
	}{
		{"folder called .env", []string{"app/.env/lib.py"}, ""},
		{"folder called .envs", []string{".envs/py/bin/x"}, ""},
		{"HEAD, objects but no refs", []string{"x/HEAD", "x/objects/o"}, ""},
		{"HEAD, refs but no objects", []string{"x/HEAD", "x/refs/r"}, ""},
		{"objects, refs and another file", []string{"x/notes.txt", "x/objects/o", "x/refs/r"}, ""},
		{"bare repository beside a file called node_modules", []string{"node_modules", "zeta/HEAD", "zeta/objects/o", "zeta/refs/r"}, "git repository"},
		{"repository after a file called node_modules", []string{"node_modules", "zeta/.git/HEAD"}, "git repository"},
		{"env file after a file called node_modules", []string{"node_modules", "zeta/.env"}, "environment file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			target := e.folder(t, "Library/Logs/A", c.files...)
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
			it := mustApply(t, e, p).Items[0]
			if c.skip == "" {
				if it.Outcome != executor.OutcomeDone {
					t.Fatalf("item = %+v, want done", it)
				}
				return
			}
			if it.Outcome != executor.OutcomeSkipped || !strings.Contains(it.Reason, c.skip) || !exists(target) {
				t.Fatalf("item = %+v, want skipped for %q", it, c.skip)
			}
		})
	}
}

// The manifest is a complete text file, and a stale temp file from an earlier
// attempt does not stop its update.
func TestManifestFileForm(t *testing.T) {
	f := newStageFixture(t, "moved", func(f *stageFixture) {
		stale := filepath.Join(theEntry(t, f.e), quarantine.ManifestName+".tmp")
		if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	rep, it := f.apply(t)
	if it.Outcome != executor.OutcomeDone || len(rep.Warnings) != 0 {
		t.Fatalf("item = %+v, warnings %v", it, rep.Warnings)
	}
	mp := quarantine.ManifestPath(quarantine.Root(f.e.storePath), it.QuarantineID)
	b, err := os.ReadFile(mp)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' || !strings.Contains(string(b), `"status": "done"`) {
		t.Errorf("manifest = %q", b)
	}
	if exists(mp + ".tmp") {
		t.Error("temp file left behind")
	}
}

// A vendored package tree inside a folder that is re-measured is still not
// checked for .git or .env, and is counted in the size.
func TestRemeasuredFolderAcceptsVendoredContents(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "a.log", "node_modules/pkg/.git/HEAD", "node_modules/pkg/.env", "lib/site-packages/x/.git/HEAD")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if err := os.WriteFile(filepath.Join(target, "new.bin"), make([]byte, 50000), 0o644); err != nil {
		t.Fatal(err)
	}
	want := allocated(t, target)
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeDone || it.MovedBytes != want {
		t.Fatalf("item = %+v, want %d bytes", it, want)
	}
}
