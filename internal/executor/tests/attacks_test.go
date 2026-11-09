package tests

// P6-R1 red-team tests (docs/tasks/reports/P6-R1.md). Every test runs in
// this package, so TestMain's MACDOC_SANDBOX applies, and every fixture is a
// folder inside a sandboxed fixture home. Native commands only reach the
// fake Exec; nothing here runs a cleanup tool.
//
// TestRedteamFixed* tests were findings (P6-R1 F1-F8) and pass now that
// they are fixed; they stay as regression tests. TestRedteamHeld* tests are
// attacks that held; they pass and stay as regression tests.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/executor"

	"golang.org/x/text/unicode/norm"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/sandbox"
	"github.com/sagnik26/macdoc/internal/store"
)

// ---------------------------------------------------------------------------
// Findings (fail until fixed)
// ---------------------------------------------------------------------------

// P6-R1 F1 (Medium): the content check and the move cross onto another
// volume. A folder with a disk image (or a FUSE, SMB or sshfs mount)
// mounted somewhere inside it passes every check: the executor compares
// only the target's own device with the quarantine's. renameatx_np moves the
// folder with the live mount inside it into quarantine, DeleteNow deletes
// the target's own files around it, and the item is reported done. The
// files on the mounted volume survive only because Go's os.RemoveAll gives
// up when unlinkat on the mount point returns EBUSY (an implementation
// detail, not a check); the entry is then stuck in quarantine with the
// mount inside it, and the content walk itself read the whole other volume.
func TestRedteamFixedNestedMountDeleted(t *testing.T) {
	if runtime.GOOS != "darwin" || testing.Short() {
		t.Skip("needs hdiutil (macOS)")
	}
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("needs hdiutil")
	}
	e := newEnv(t)
	e.cfg.DeleteNow = true
	target := e.folder(t, "Library/Logs/A", "a.log")
	mnt := filepath.Join(target, "mnt")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(t.TempDir(), "rt.dmg")
	run(t, "hdiutil", "create", "-quiet", "-size", "2m", "-fs", "HFS+", "-volname", "MacdocRT", "-o", img)
	run(t, "hdiutil", "attach", "-quiet", "-nobrowse", "-noautoopen", "-mountpoint", mnt, img)
	// Registered after newEnv's cleanup, so it runs first: unmount the
	// fixture volume wherever the move left it, before the home is removed.
	t.Cleanup(func() { unmountUnder(t, e.home, img) })
	precious := filepath.Join(mnt, "precious.txt")
	if err := os.WriteFile(precious, []byte("data on another volume"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	rep := mustApply(t, e, p)
	it := rep.Items[0]
	t.Logf("item = %s (%s), warnings = %v", it.Outcome, it.Reason, rep.Warnings)

	payloadCopy := ""
	if it.QuarantineID != "" {
		payloadCopy = filepath.Join(quarantine.Root(e.storePath), it.QuarantineID, "payload", "A", "mnt", "precious.txt")
	}
	if !exists(precious) && (payloadCopy == "" || !exists(payloadCopy)) {
		t.Errorf("a file on a separately mounted volume inside the target was deleted (item %s)", it.Outcome)
	}
	if it.Outcome == executor.OutcomeDone {
		t.Errorf("a folder with another volume mounted inside it was moved; want skipped")
	}
}

// P6-R1 F2 (Medium): the content check compares names case-sensitively,
// but APFS is case-insensitive by default and the safety rules (and git,
// and dotenv loaders, through the filesystem) fold case. A folder holding
// ".ENV" or a ".Git" repository passes the check and is moved (and, with
// DeleteNow, deleted), while safety.CheckTarget refuses the same names as
// targets.
func TestRedteamFixedContentCheckIgnoresCase(t *testing.T) {
	for _, c := range []struct{ name, file string }{
		{"upper-case env file", "app/.ENV"},
		{"mixed-case env file", "app/.Env.local"},
		{"upper-case git dir", "proj/.GIT/HEAD"},
		{"mixed-case git file (worktree)", "proj/.Git"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.cfg.DeleteNow = true
			target := e.folder(t, "Library/Logs/A", c.file)
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
			it := mustApply(t, e, p).Items[0]
			if it.Outcome != executor.OutcomeSkipped || !exists(filepath.Join(target, c.file)) {
				t.Fatalf("folder holding %s: item = %s (deleted %v), want skipped and untouched", c.file, it.Outcome, it.Deleted)
			}
		})
	}
}

// P6-R1 F3 (Low): a bare repository whose HEAD is a symbolic link (what
// git writes with core.preferSymlinkRefs, and still reads) isn't
// recognized: the check wants HEAD to be a regular file.
func TestRedteamFixedBareRepoSymlinkHead(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "backup.git/objects/ab/cdef", "backup.git/refs/heads/main", "backup.git/config")
	mustSymlink(t, "refs/heads/main", filepath.Join(target, "backup.git", "HEAD"))
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeSkipped || !exists(filepath.Join(target, "backup.git", "objects")) {
		t.Fatalf("bare repo with a symlinked HEAD: item = %s, want skipped", it.Outcome)
	}
}

// P6-R1 F4 (Medium): ensureRoot (and tightenStateDir) follow a symlink at
// the quarantine root. If <state>/quarantine is a symlink to a protected
// folder, apply chmods that folder to 0700, drops .metadata_never_index in
// it (Spotlight stops indexing it; outside sandbox mode `tmutil
// addexclusion` would be run on the path too), creates the Q-XXXX entry
// there and moves the target into it. sandbox.Check(root) passes because
// Canonicalize doesn't follow the final component. executor.Purge later refuses the
// entry (its canonical path differs), so the moved item stays inside
// ~/Public.
func TestRedteamFixedSymlinkedQuarantineRoot(t *testing.T) {
	e := newEnv(t)
	e.cfg.DeleteNow = true
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	public := e.folder(t, "Public", "shared.txt")
	if err := os.Chmod(public, 0o755); err != nil { //nolint:gosec // G302: fixture mirrors ~/Public's default mode.
		t.Fatal(err)
	}
	mustSymlink(t, public, quarantine.Root(e.storePath))

	rep, err := executor.Apply(context.Background(), e.cfg, p, all(p))
	if err == nil {
		t.Logf("item = %+v, warnings = %v", rep.Items[0], rep.Warnings)
	}
	if fi, _ := os.Stat(public); fi.Mode().Perm() != 0o755 {
		t.Errorf("~/Public mode changed to %v by following the quarantine symlink", fi.Mode().Perm())
	}
	if exists(filepath.Join(public, ".metadata_never_index")) {
		t.Error("Spotlight exclusion marker written into ~/Public")
	}
	if ents, _ := filepath.Glob(filepath.Join(public, "Q-*")); len(ents) != 0 {
		t.Errorf("quarantine entry created inside ~/Public: %v", ents)
	}
	if !exists(filepath.Join(target, "a.log")) {
		t.Error("the target was moved into a protected folder")
	}
}

// P6-R1 F5 (Low): the after-move check can't see a swapped ancestor.
// sameDir re-resolves the parent path through the same swapped symlink, so
// it names the opened folder again. With a regular-file target that has a
// hard link in ~/Documents, swapping an ancestor of the target's folder for
// a symlink into ~/Documents (after the checks, before the move) makes the
// executor unlink the ~/Documents name and delete it. The file's data
// survives through the other link, but an entry in a protected folder was
// removed. Needs a racing same-user process.
func TestRedteamFixedAncestorSwapDuringMove(t *testing.T) {
	e := newEnv(t)
	e.cfg.DeleteNow = true
	dd := filepath.Join(e.home, "Library/Developer/Xcode/DerivedData")
	if err := os.MkdirAll(dd, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dd, "notes.bin")
	if err := os.WriteFile(target, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(e.home, "Documents/Dev/Xcode/DerivedData")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(docs, "notes.bin")
	if err := os.Link(target, victim); err != nil {
		t.Fatal(err)
	}
	p := e.plan(t, e.item(t, "O-aaaaaa", "xcode.derived_data", model.RiskSafe, target))
	dev := filepath.Join(e.home, "Library/Developer")
	e.cfg.StageHook = func(stage string) {
		if stage == "pending" {
			mustRename(t, dev, dev+".moved")
			mustSymlink(t, filepath.Join(e.home, "Documents/Dev"), dev)
		}
	}
	it := mustApply(t, e, p).Items[0]
	if !exists(victim) {
		t.Fatalf("an entry inside ~/Documents was moved and deleted (item %s, deleted %v)", it.Outcome, it.Deleted)
	}
}

// P6-R1 F6 (Low): with MACDOC_SANDBOX set, apply still changes things
// outside it: tightenStateDir chmods the state directory and ensureRoot
// creates and chmods <state>/quarantine before sandbox.Check(root) runs.
// The sandbox contract is "refuse to touch any path outside the sandbox".
func TestRedteamFixedSandboxStateDirOutside(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	// Canonical (EvalSymlinks), so the only reason to refuse is the sandbox
	// itself, not a non-canonical path (P6-R1 F6).
	outside, err := filepath.EvalSymlinks(t.TempDir()) // not inside MACDOC_SANDBOX
	if err != nil {
		t.Fatal(err)
	}
	if root, _, _ := sandbox.Root(); strings.HasPrefix(outside, root) {
		t.Skip("t.TempDir is inside the sandbox here")
	}
	stateDir := filepath.Join(outside, "state")
	sp := filepath.Join(stateDir, "state.db")
	st, err := store.Open(sp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e.st, e.storePath, e.cfg.Store, e.cfg.StorePath = st, sp, st, sp
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if err := os.Chmod(stateDir, 0o755); err != nil { //nolint:gosec // G302: fixture mode to detect a change.
		t.Fatal(err)
	}

	_, _ = executor.Apply(context.Background(), e.cfg, p, all(p))
	if !exists(filepath.Join(target, "a.log")) {
		t.Fatal("target moved")
	}
	if fi, _ := os.Stat(stateDir); fi.Mode().Perm() != 0o755 {
		t.Errorf("state directory outside the sandbox was chmodded to %v", fi.Mode().Perm())
	}
	if exists(quarantine.Root(sp)) {
		t.Error("quarantine folder created outside the sandbox")
	}
}

// P6-R1 F7 (Low): purge deletes permanently even when the audit log can't
// be written; the deletion leaves no record. executor.Apply refuses to start without
// an audit trail (apply_started), purge and restore don't check first.
func TestRedteamFixedPurgeWithoutAudit(t *testing.T) {
	e := newEnv(t)
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
	qid := mustApply(t, e, p).Items[0].QuarantineID
	logPath := audit.Path(e.storePath)
	if err := os.Chmod(logPath, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(logPath, 0o600) })
	before := len(e.events(t))

	res, err := executor.Purge(context.Background(), e.cfg, []string{qid})
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(quarantine.Root(e.storePath), qid)
	if !exists(entry) {
		t.Errorf("entry permanently deleted with no audit record (outcome %s: %q)", res[0].Outcome, res[0].Reason)
	}
	if n := len(e.events(t)); n != before {
		t.Errorf("audit grew from %d to %d records", before, n)
	}
}

// P6-R1 F8 (Low, availability): stillListed re-runs the recipe's detector
// without scan nodes, and the android_avds and simctl_idle_devices
// detectors return nothing when Env.Nodes is nil. So every
// `avdmanager delete avd -n {id}` and `xcrun simctl delete {id}` item is
// skipped as "no longer listed", even when the AVD is right there. It fails
// closed, but those two recipes can never run.
func TestRedteamFixedRelistNeedsScan(t *testing.T) {
	withTools(t, "avdmanager")
	t.Setenv("ANDROID_AVD_HOME", "")
	t.Setenv("ANDROID_EMULATOR_HOME", "")
	e := newEnv(t)
	avdDir := e.folder(t, ".android/avd/Pixel_5.avd", "config.ini", "userdata.img")
	if err := os.WriteFile(filepath.Join(avdDir, "config.ini"), []byte("avd.ini.displayname=Pixel 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ini := "avd.ini.encoding=UTF-8\npath=" + avdDir + "\ntarget=android-34\n"
	if err := os.WriteFile(filepath.Join(e.home, ".android/avd/Pixel_5.ini"), []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}
	p := e.plan(t, e.native(t, "O-aaaaaa", "android.avd", model.RiskCaution, "avdmanager", "delete", "avd", "-n", "Pixel_5"))
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeDone {
		t.Fatalf("an AVD that exists: item = %s (%s), want done", it.Outcome, it.Reason)
	}
}

// ---------------------------------------------------------------------------
// Attacks that held (regression tests)
// ---------------------------------------------------------------------------

// Symlinks inside a target point at protected data: moving and deleting
// the folder removes the links, never what they point to.
func TestRedteamHeldSymlinksInsideTarget(t *testing.T) {
	e := newEnv(t)
	e.cfg.DeleteNow = true
	thesis := e.folder(t, "Documents/Thesis", "chapter1.md")
	target := e.folder(t, "Library/Logs/A")
	mustSymlink(t, thesis, filepath.Join(target, "docs"))
	mustSymlink(t, filepath.Join(thesis, "chapter1.md"), filepath.Join(target, "ch1"))
	mustSymlink(t, "/", filepath.Join(target, "root"))
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if it := mustApply(t, e, p).Items[0]; it.Outcome != executor.OutcomeDone || !it.Deleted {
		t.Fatalf("item = %+v", it)
	}
	if !exists(filepath.Join(thesis, "chapter1.md")) {
		t.Fatal("a symlink's destination was deleted")
	}
}

// A regular-file target hard-linked from ~/Documents: only the cache's name
// is removed; the document keeps its data.
func TestRedteamHeldHardLinkedFile(t *testing.T) {
	e := newEnv(t)
	e.cfg.DeleteNow = true
	doc := filepath.Join(e.folder(t, "Documents/Notes", "x"), "plan.md")
	if err := os.WriteFile(doc, []byte("my notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(e.home, "Library/Logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(logs, "old.log")
	if err := os.Link(doc, target); err != nil {
		t.Fatal(err)
	}
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if it := mustApply(t, e, p).Items[0]; it.Outcome != executor.OutcomeDone {
		t.Fatalf("item = %+v", it)
	}
	if b, err := os.ReadFile(doc); err != nil || string(b) != "my notes" {
		t.Fatalf("hard-linked document lost: %q, %v", b, err)
	}
}

// Names that are awkward at apply time: NFD on disk with an NFC plan path,
// newlines, escapes, trailing spaces and dots, and a case-only rename of
// the same folder. Each moves and restores to the same place.
func TestRedteamHeldOddNamesRoundTrip(t *testing.T) {
	for _, c := range []struct{ name, onDisk, planned string }{
		{"NFD on disk", norm.NFD.String("Café"), norm.NFC.String("Café")},
		{"newline", "A\nB", "A\nB"},
		{"terminal escape", "A\x1b[2JB", "A\x1b[2JB"},
		{"trailing space", "A ", "A "},
		{"trailing dot", "A.", "A."},
		{"case-only rename after plan", "abc", "Abc"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			planned := filepath.Join(e.home, "Library/Logs", c.planned)
			disk := filepath.Join(e.home, "Library/Logs", c.onDisk)
			if c.name == "case-only rename after plan" {
				e.folder(t, "Library/Logs/"+c.planned)
			} else {
				e.folder(t, "Library/Logs/"+c.onDisk)
			}
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, planned))
			if c.name == "case-only rename after plan" {
				mustRename(t, planned, disk)
			}
			it := mustApply(t, e, p).Items[0]
			if it.Outcome != executor.OutcomeDone {
				t.Fatalf("item = %+v", it)
			}
			if exists(disk) {
				t.Fatal("still there after the move")
			}
			if _, err := executor.Restore(context.Background(), e.cfg, it.QuarantineID); err != nil {
				t.Fatal(err)
			}
			if !exists(filepath.Join(planned, "a.log")) {
				t.Fatal("not restored to its place")
			}
		})
	}
}

// Repositories hidden deeper, as worktrees or as submodules, and a bare repo
// below the top: all refused.
func TestRedteamHeldRepositoriesDeep(t *testing.T) {
	for name, files := range map[string][]string{
		"deep .git":              {"a/b/c/d/e/f/g/h/proj/.git/HEAD"},
		"worktree .git file":     {"x/wt/.git"},
		"submodule .git file":    {"p/vendor/sub/.git", "p/vendor/sub/README"},
		"bare repo below top":    {"a/b/r.git/HEAD", "a/b/r.git/objects/x", "a/b/r.git/refs/tags/v1"},
		"env file deep":          {"a/b/c/.env.production"},
		"env inside vendor-like": {"node_modulesX/.envrc"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.cfg.DeleteNow = true
			target := e.folder(t, "Library/Logs/A", files...)
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
			if it := mustApply(t, e, p).Items[0]; it.Outcome != executor.OutcomeSkipped || !exists(filepath.Join(target, files[0])) {
				t.Fatalf("item = %+v", it)
			}
		})
	}
}

// A forged plan (file and store row both written by an attacker) can't
// carry a protected path, a path outside home, a move with a command, or a
// command the recipe doesn't allow.
func TestRedteamHeldForgedPlans(t *testing.T) {
	cases := map[string]func(t *testing.T, e *env, it *model.PlanItem){
		"documents target": func(t *testing.T, e *env, it *model.PlanItem) {
			it.Target = e.folder(t, "Documents/Thesis")
		},
		"outside home": func(_ *testing.T, _ *env, it *model.PlanItem) { it.Target = "/private/tmp" },
		"move with argv": func(_ *testing.T, _ *env, it *model.PlanItem) {
			it.Argv = []string{"brew", "cleanup", "--prune=all"}
		},
		"native method on move recipe": func(_ *testing.T, _ *env, it *model.PlanItem) {
			it.Method, it.Argv = model.MethodNative, []string{"brew", "cleanup", "--prune=all"}
		},
		"lower risk than recipe": func(t *testing.T, e *env, it *model.PlanItem) {
			it.RecipeID, it.Risk, it.Target = "user.app_caches", model.RiskSafe, e.folder(t, "Library/Caches/com.x")
		},
		"home itself": func(_ *testing.T, e *env, it *model.PlanItem) { it.Target = e.home },
		"state dir":   func(_ *testing.T, e *env, it *model.PlanItem) { it.Target = filepath.Dir(e.storePath) },
	}
	for name, forge := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			target := e.folder(t, "Library/Logs/A")
			it := e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target)
			forge(t, e, &it)
			p := e.plan(t, it) // sealed and recorded: a forged file + row
			if _, err := executor.Apply(context.Background(), e.cfg, p, all(p)); !errors.Is(err, planner.ErrInvalid) {
				t.Fatalf("apply = %v, want ErrInvalid", err)
			}
			if len(e.events(t)) != 0 || len(e.exec.calls) != 0 {
				t.Fatal("a forged plan changed something")
			}
			if !exists(filepath.Join(target, "a.log")) || (strings.HasPrefix(it.Target, e.home+"/") && !exists(it.Target)) {
				t.Fatal("a forged plan moved something")
			}
		})
	}
}

// Argument injection through {id}: anything that isn't a plain identifier
// is refused at Verify, and an identifier that is only a prefix of a listed
// one is refused at the re-listing. The command never runs.
func TestRedteamHeldNativeArgInjection(t *testing.T) {
	withTools(t, "ollama")
	for _, id := range []string{"-rf", "--force", "-", "all", "ALL", "a b", "x;rm", "a/b", "../x", "$(id)", "a\tb", "a\nb",
		"\x1b]0;x\a", "", "llama3.2:latest\x00", "help", "--model=x"} {
		t.Run(strings.ToValidUTF8(id, "?"), func(t *testing.T) {
			e := newEnv(t)
			p := e.plan(t, e.native(t, "O-aaaaaa", "ollama.model", model.RiskCaution, "ollama", "rm", id))
			if _, err := executor.Apply(context.Background(), e.cfg, p, all(p)); !errors.Is(err, planner.ErrInvalid) {
				t.Fatalf("apply = %v, want ErrInvalid", err)
			}
			if len(e.exec.calls) != 0 {
				t.Fatalf("ran %v", e.exec.calls)
			}
		})
	}
	t.Run("prefix of a listed model", func(t *testing.T) {
		e := newEnv(t)
		e.runner.Responses["ollama list"] = platform.Result{Stdout: []byte("NAME   ID   SIZE   MODIFIED\nllama3.2:latest  a80c  2.0 GB  3 weeks ago\n")}
		e.runner.Responses["ollama ps"] = platform.Result{Stdout: []byte("NAME ID SIZE PROCESSOR UNTIL\n")}
		p := e.plan(t, e.native(t, "O-aaaaaa", "ollama.model", model.RiskCaution, "ollama", "rm", "llama3.2"))
		if it := mustApply(t, e, p).Items[0]; it.Outcome != executor.OutcomeSkipped || len(e.exec.calls) != 0 {
			t.Fatalf("item = %+v, ran %v", it, e.exec.calls)
		}
	})
}

// The quarantine root swapped for a symlink after a move: purge refuses
// (the entry's canonical path changed) and the data stays.
func TestRedteamHeldPurgeQuarantineRootSwapped(t *testing.T) {
	e := newEnv(t)
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
	qid := mustApply(t, e, p).Items[0].QuarantineID
	root := quarantine.Root(e.storePath)
	real := filepath.Join(e.home, "Documents", "q")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRename(t, root, real)
	mustSymlink(t, real, root)
	res, _ := executor.Purge(context.Background(), e.cfg, []string{qid})
	if res[0].Outcome == executor.OutcomeDone || !exists(filepath.Join(real, qid, "payload", "A", "a.log")) {
		t.Fatalf("purge through a symlinked root = %+v", res[0])
	}
	if _, err := executor.Restore(context.Background(), e.cfg, qid); err == nil {
		t.Error("restored through a symlinked root")
	}
}

// Forged store rows and entries: a row with no folder, a row whose entry
// folder is a symlink into ~/Documents, and a payload folder replaced by a
// symlink. None of them deletes or restores anything outside quarantine.
func TestRedteamHeldForgedQuarantineEntries(t *testing.T) {
	e := newEnv(t)
	thesis := e.folder(t, "Documents/Thesis", "chapter1.md")
	root := quarantine.Root(e.storePath)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	add := func(id string) {
		t.Helper()
		if err := e.st.AddQuarantine(store.QuarantineRecord{ID: id, PlanID: "P-AAAA", ItemID: "O-aaaaaa", RecipeID: "user.logs",
			Original: thesis, Bytes: 1, CreatedAt: t0, ExpiresAt: t0.Add(quarantine.TTL)}); err != nil {
			t.Fatal(err)
		}
	}
	add("Q-AAAA") // no folder at all
	add("Q-BBBB") // entry folder is a symlink to ~/Documents
	mustSymlink(t, filepath.Dir(thesis), filepath.Join(root, "Q-BBBB"))

	// A real entry whose payload folder is swapped for a symlink.
	p := e.plan(t, e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
	qid := mustApply(t, e, p).Items[0].QuarantineID
	pl := filepath.Join(root, qid, "payload")
	mustRename(t, pl, pl+".x")
	docA := e.folder(t, "Documents/A", "keep.md")
	mustSymlink(t, filepath.Dir(docA), pl)

	res, err := executor.Purge(context.Background(), e.cfg, []string{"Q-AAAA", "Q-BBBB", qid})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.ID != "Q-AAAA" && r.Outcome == executor.OutcomeDone {
			t.Errorf("purged %s: %+v", r.ID, r)
		}
	}
	for _, id := range []string{"Q-BBBB", qid} {
		if _, err := executor.Restore(context.Background(), e.cfg, id); err == nil {
			t.Errorf("restored forged entry %s", id)
		}
	}
	if !exists(filepath.Join(thesis, "chapter1.md")) || !exists(filepath.Join(docA, "keep.md")) {
		t.Fatal("something outside quarantine was touched")
	}
}

// executor.Restore into a parent that became a symlink into ~/Documents, and onto a
// dangling symlink at the original path: both refused, nothing written.
func TestRedteamHeldRestoreRedirects(t *testing.T) {
	t.Run("parent is a symlink", func(t *testing.T) {
		e := newEnv(t)
		target := e.folder(t, "Library/Logs/App/A")
		p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, filepath.Dir(target)))
		qid := mustApply(t, e, p).Items[0].QuarantineID
		logs := filepath.Join(e.home, "Library/Logs")
		docs := e.folder(t, "Documents/Logs", "x")
		mustRename(t, logs, logs+".old")
		mustSymlink(t, docs, logs)
		if _, err := executor.Restore(context.Background(), e.cfg, qid); err == nil {
			t.Fatal("restored through a symlinked parent")
		}
		if exists(filepath.Join(docs, "App")) {
			t.Fatal("restore wrote into ~/Documents")
		}
	})
	t.Run("dangling symlink at the original", func(t *testing.T) {
		e := newEnv(t)
		target := e.folder(t, "Library/Logs/A")
		p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
		qid := mustApply(t, e, p).Items[0].QuarantineID
		mustSymlink(t, filepath.Join(e.home, "Documents/New"), target)
		if _, err := executor.Restore(context.Background(), e.cfg, qid); !errors.Is(err, executor.ErrNotRestorable) {
			t.Fatalf("restore onto a symlink = %v", err)
		}
		if exists(filepath.Join(e.home, "Documents/New")) {
			t.Fatal("restore followed the symlink")
		}
	})
}

// Two purges of one entry at once, and two plans for the same target
// applied at once: one wins, nothing else is touched, no entry is left half
// done.
func TestRedteamHeldConcurrentPurgeAndApply(t *testing.T) {
	t.Run("purge twice", func(t *testing.T) {
		e := newEnv(t)
		p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
		qid := mustApply(t, e, p).Items[0].QuarantineID
		var wg sync.WaitGroup
		out := make([][]executor.EntryResult, 2)
		for i := range out {
			wg.Add(1)
			go func() { defer wg.Done(); out[i], _ = executor.Purge(context.Background(), e.cfg, []string{qid}) }()
		}
		wg.Wait()
		done := 0
		for _, r := range out {
			if r[0].Outcome == executor.OutcomeDone {
				done++
			}
		}
		if done == 0 || exists(filepath.Join(quarantine.Root(e.storePath), qid)) {
			t.Fatalf("purges = %+v", out)
		}
	})
	t.Run("two plans, one target", func(t *testing.T) {
		e := newEnv(t)
		target := e.folder(t, "Library/Logs/A", "a.log", "b/c.log")
		p1 := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
		p2 := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
		var wg sync.WaitGroup
		reps := make([]executor.Report, 2)
		for i, p := range []model.Plan{p1, p2} {
			wg.Add(1)
			go func() { defer wg.Done(); reps[i], _ = executor.Apply(context.Background(), e.cfg, p, all(p)) }()
		}
		wg.Wait()
		done := 0
		for _, r := range reps {
			if len(r.Items) == 1 && r.Items[0].Outcome == executor.OutcomeDone {
				done++
			}
		}
		l, _ := quarantine.List(e.st, quarantine.Root(e.storePath), e.cfg.Now())
		if done != 1 || len(l.Entries) != 1 || l.Entries[0].State != quarantine.Held {
			t.Fatalf("done = %d, entries = %+v", done, l.Entries)
		}
	})
}

// A set but malformed MACDOC_SANDBOX makes apply, restore and purge refuse
// before changing anything.
func TestRedteamHeldMalformedSandbox(t *testing.T) {
	for _, v := range []string{"relative/dir", "/nonexistent-macdoc-rt", "/", " ", "/private/tmp"} {
		t.Run(v, func(t *testing.T) {
			e := newEnv(t)
			target := e.folder(t, "Library/Logs/A")
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
			kept := e.plan(t, e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/B")))
			qid := mustApply(t, e, kept).Items[0].QuarantineID
			n := len(e.events(t))

			t.Setenv(sandbox.Env, v)
			if _, err := executor.Apply(context.Background(), e.cfg, p, all(p)); err == nil {
				t.Error("apply ran")
			}
			if _, err := executor.Restore(context.Background(), e.cfg, qid); err == nil {
				t.Error("restore ran")
			}
			if _, err := executor.Purge(context.Background(), e.cfg, []string{qid}); err == nil {
				t.Error("purge ran")
			}
			if !exists(filepath.Join(target, "a.log")) || !exists(filepath.Join(quarantine.Root(e.storePath), qid, "payload", "B")) {
				t.Fatal("something changed")
			}
			if len(e.events(t)) != n {
				t.Error("audit written")
			}
		})
	}
}

// approved holding IDs that aren't in the plan acts on nothing extra; a
// plan item that isn't approved is never touched.
func TestRedteamHeldApprovedExtraIDs(t *testing.T) {
	e := newEnv(t)
	a := e.folder(t, "Library/Logs/A")
	b := e.folder(t, "Library/Logs/B")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, a), e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, b))
	rep, err := executor.Apply(context.Background(), e.cfg, p, executor.Approvals{"O-aaaaaa": {Individually: true}, "O-zzzzzz": {Individually: true}, "": {Individually: true}, "../x": {Individually: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 2 || rep.Items[1].Outcome != executor.OutcomeSkipped || !exists(b) || exists(a) {
		t.Fatalf("items = %+v", rep.Items)
	}
}

// If apply_started can't be written nothing changes (the plan is used up,
// by design: the lock is taken first).
func TestRedteamHeldNoAuditNoApply(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if err := os.MkdirAll(audit.Path(e.storePath), 0o700); err != nil { // a folder where the log should be
		t.Fatal(err)
	}
	if _, err := executor.Apply(context.Background(), e.cfg, p, all(p)); err == nil {
		t.Fatal("apply ran without an audit log")
	}
	if !exists(filepath.Join(target, "a.log")) {
		t.Fatal("target moved")
	}
	if rec, _ := e.st.Plan(p.ID); rec.Status != store.PlanApplied {
		t.Logf("plan status %s", rec.Status)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Skipf("%s %v: %v: %s", name, args, err, out)
	}
}

// unmountUnder force-unmounts every volume mounted below dir (the fixture
// disk image, wherever the move left it), then detaches the image.
func unmountUnder(t *testing.T, dir, img string) {
	t.Helper()
	if out, err := exec.Command("mount").Output(); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			_, rest, ok := strings.Cut(sc.Text(), " on ")
			if !ok {
				continue
			}
			mp, _, _ := strings.Cut(rest, " (")
			if strings.Contains(mp, filepath.Base(dir)) {
				if out, err := exec.Command("umount", "-f", mp).CombinedOutput(); err != nil {
					t.Logf("umount %s: %v %s", mp, err, out)
				}
			}
		}
	}
	// Detach the image's device (hdiutil info lists it after its image-path).
	out, err := exec.Command("hdiutil", "info").Output()
	if err != nil {
		return
	}
	mine := false
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "image-path"):
			mine = strings.HasSuffix(strings.TrimSpace(line), img)
		case mine && strings.HasPrefix(line, "/dev/disk"):
			dev := strings.Fields(line)[0]
			if out, err := exec.Command("hdiutil", "detach", "-force", dev).CombinedOutput(); err != nil {
				t.Logf("detach %s: %v %s", dev, err, out)
			}
			return
		}
	}
}

// P6-R1 F1, purge side: a disk mounted inside a quarantined entry (after the
// move, so the move-time check never saw it) makes purge refuse the entry.
// Needs hdiutil (macOS); skipped elsewhere.
func TestRedteamFixedMountInsideEntry(t *testing.T) {
	if runtime.GOOS != "darwin" || testing.Short() {
		t.Skip("needs hdiutil (macOS)")
	}
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("needs hdiutil")
	}
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "a.log")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	qid := mustApply(t, e, p).Items[0].QuarantineID
	if qid == "" {
		t.Fatal("setup: the target wasn't quarantined")
	}
	payloadA := filepath.Join(quarantine.Root(e.storePath), qid, "payload", "A")
	mnt := filepath.Join(payloadA, "mnt")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(t.TempDir(), "rt2.dmg")
	run(t, "hdiutil", "create", "-quiet", "-size", "2m", "-fs", "HFS+", "-volname", "MacdocRT2", "-o", img)
	run(t, "hdiutil", "attach", "-quiet", "-nobrowse", "-noautoopen", "-mountpoint", mnt, img)
	t.Cleanup(func() { unmountUnder(t, e.home, img) })
	precious := filepath.Join(mnt, "precious.txt")
	if err := os.WriteFile(precious, []byte("data on another volume"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := executor.Purge(context.Background(), e.cfg, []string{qid})
	if err != nil {
		t.Fatal(err)
	}
	// Refused by the check (skipped, reason names the disk), not failed by
	// accident: RemoveAll's EBUSY on the mount point would also keep the file.
	if res[0].Outcome != executor.OutcomeSkipped || !strings.Contains(res[0].Reason, "another disk") || !exists(precious) {
		t.Fatalf("purge of an entry with another volume inside: outcome %s (%q), file kept %v; want skipped for the mounted disk and file kept", res[0].Outcome, res[0].Reason, exists(precious))
	}
}

// P6-R1 F5, the second layer on its own: the parent folder's ancestor is
// swapped for a symlink after macdoc opened the folder and before the move.
// The check before the move (sameDir) must refuse the item; the move must
// not run from a folder that no longer matches the canonical path.
func TestRedteamFixedAncestorSwapAfterOpen(t *testing.T) {
	e := newEnv(t)
	e.cfg.DeleteNow = true
	dd := filepath.Join(e.home, "Library/Developer/Xcode/DerivedData")
	if err := os.MkdirAll(dd, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dd, "notes.bin")
	if err := os.WriteFile(target, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(e.home, "Documents/Dev/Xcode/DerivedData")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(docs, "notes.bin")
	if err := os.Link(target, victim); err != nil {
		t.Fatal(err)
	}
	p := e.plan(t, e.item(t, "O-aaaaaa", "xcode.derived_data", model.RiskSafe, target))
	dev := filepath.Join(e.home, "Library/Developer")
	e.cfg.StageHook = func(stage string) {
		if stage == "opened" {
			mustRename(t, dev, dev+".moved")
			mustSymlink(t, filepath.Join(e.home, "Documents/Dev"), dev)
		}
	}
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != executor.OutcomeSkipped {
		t.Fatalf("ancestor swapped after the parent was opened: item = %s (%s), want skipped", it.Outcome, it.Reason)
	}
	if !exists(victim) {
		t.Fatal("an entry inside ~/Documents was moved")
	}
}

// executor.Purge removes the payload before the manifest: the manifest is still there
// when the payload is gone, so an interrupted purge stays accountable.
func TestRedteamFixedPurgeCrashOrder(t *testing.T) {
	e := newEnv(t)
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
	qid := mustApply(t, e, p).Items[0].QuarantineID
	entry := quarantine.EntryDir(quarantine.Root(e.storePath), qid)
	seen := false
	e.cfg.StageHook = func(stage string) {
		if stage == "purged-payload" {
			seen = true
			if !exists(filepath.Join(entry, quarantine.ManifestName)) {
				t.Error("the manifest was removed before the payload was purged")
			}
			if exists(filepath.Join(entry, quarantine.Payload)) {
				t.Error("the payload still exists at the point of the manifest check")
			}
		}
	}
	if _, err := executor.Purge(context.Background(), e.cfg, []string{qid}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("purge did not reach the payload step")
	}
	if exists(entry) {
		t.Error("the entry folder is still there after purge")
	}
}

// executor.Purge and restore hold one lock on the quarantine: a second taker waits
// until the first releases it.
func TestQuarantineLockExcludes(t *testing.T) {
	dir := t.TempDir()
	unlock, err := executor.LockQuarantine(dir)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan struct{})
	go func() {
		u, err := executor.LockQuarantine(dir)
		if err == nil {
			close(acquired)
			u()
		}
	}()
	select {
	case <-acquired:
		t.Fatal("a second lock was taken while the first was held")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("the second lock was never taken after release")
	}
}

// executor.Restore re-checks the recipe: an item whose recipe is no longer in the
// catalog is refused, and nothing moves.
func TestRestoreRefusedWithoutRecipe(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	qid := mustApply(t, e, p).Items[0].QuarantineID
	e.cfg.Catalog = &recipes.Catalog{}
	if _, err := executor.Restore(context.Background(), e.cfg, qid); !errors.Is(err, executor.ErrNotRestorable) {
		t.Fatalf("restore with the recipe gone = %v, want executor.ErrNotRestorable", err)
	}
	if exists(target) {
		t.Fatal("restore moved the folder back although its recipe is gone")
	}
}
