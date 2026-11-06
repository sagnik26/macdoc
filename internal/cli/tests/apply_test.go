package tests

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/sandbox"
	"github.com/sagnik26/macdoc/internal/store"
)

var applyNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// applyEnv is a sandboxed home with a store, for apply and quarantine
// command tests. Every test here runs under MACDOC_SANDBOX.
type applyEnv struct {
	Home string
	st   *store.Store
	cat  *recipes.Catalog
}

func newApplyEnv(t *testing.T) *applyEnv {
	t.Helper()
	sb, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(sandbox.Env, sb)
	home := filepath.Join(sb, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.DefaultPath(home))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	old := cli.Runner
	cli.Runner = &platform.FakeRunner{Responses: map[string]platform.Result{"ps -axww -o command=": {Stdout: []byte("/sbin/launchd\n")}}}
	t.Cleanup(func() { cli.Runner = old })
	tty := cli.RealTTY
	cli.RealTTY = func(io.Reader, io.Writer) bool { return true }
	t.Cleanup(func() { cli.RealTTY = tty })
	return &applyEnv{Home: home, st: st, cat: cat}
}

func (e *applyEnv) folder(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(e.Home, rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "f"), make([]byte, 3000), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *applyEnv) item(t *testing.T, id, recipe string, risk model.Risk, target string) model.PlanItem {
	t.Helper()
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := e.cat.Get(recipe)
	return model.PlanItem{OpportunityID: id, RecipeID: recipe, Title: r.Title, Target: target, ExpectedBytes: 4096,
		BytesSource: model.SourceMeasured, Risk: risk, Method: model.MethodQuarantine, Preconditions: r.Preconditions,
		Restore: r.Restore, Fingerprint: planner.Fingerprint(fi)}
}

func (e *applyEnv) Plan(t *testing.T, origin model.Origin, items ...model.PlanItem) model.Plan {
	t.Helper()
	p := model.Plan{SchemaVersion: model.SchemaVersion, CreatedAt: applyNow, ExpiresAt: applyNow.Add(planner.DefaultTTL),
		Origin: origin, ScanID: "S-1", Request: "--safe", Items: items}
	if err := planner.Save(e.st, planner.Dir(store.DefaultPath(e.Home)), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// apply runs `macdoc apply id` with input typed at the prompts.
func (e *applyEnv) apply(t *testing.T, id, input string) (string, error) {
	t.Helper()
	return e.run(t, id, input, false)
}

// applyKeep applies but leaves moved items in quarantine, for the
// quarantine command tests.
func (e *applyEnv) applyKeep(t *testing.T, id, input string) (string, error) {
	t.Helper()
	return e.run(t, id, input, true)
}

func (e *applyEnv) run(t *testing.T, id, input string, keep bool) (string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(&out)
	err := cli.RunApply(cmd, cli.ApplyDeps{Home: e.Home, Now: func() time.Time { return applyNow.Add(time.Minute) }, Keep: keep}, id)
	return out.String(), err
}

func (e *applyEnv) status(t *testing.T, id string) store.PlanStatus {
	t.Helper()
	r, err := e.st.Plan(id)
	if err != nil {
		t.Fatal(err)
	}
	return r.Status
}

func present(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestApplyNeedsATerminal(t *testing.T) {
	e := newApplyEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.Plan(t, model.OriginCLI, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	cli.RealTTY = func(io.Reader, io.Writer) bool { return false }
	if _, err := e.apply(t, p.ID, strings.TrimPrefix(p.ID, "P-")+"\n"); err == nil || !strings.Contains(err.Error(), "Terminal") {
		t.Fatalf("err = %v", err)
	}
	if !present(target) || e.status(t, p.ID) != store.PlanPending {
		t.Fatal("something changed without a terminal")
	}
	// The real check refuses a pipe and /dev/null.
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devnull.Close() }()
	if tty(devnull, devnull) {
		t.Error("/dev/null counted as a terminal")
	}
	r, w, _ := os.Pipe()
	defer func() { _ = r.Close(); _ = w.Close() }()
	if tty(r, w) {
		t.Error("a pipe counted as a terminal")
	}
}

// tty is the real check, kept here because tests swap cli.RealTTY.
var tty = cli.RealTTY

func TestApplyWrongCode(t *testing.T) {
	e := newApplyEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.Plan(t, model.OriginCLI, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	for _, in := range []string{"nope\n", "\n", "", p.ID + "\n"} {
		out, err := e.apply(t, p.ID, in)
		if err != nil || !strings.Contains(out, "Not confirmed. Nothing has been changed.") {
			t.Fatalf("input %q: err %v, out:\n%s", in, err, out)
		}
	}
	if !present(target) || e.status(t, p.ID) != store.PlanPending {
		t.Fatal("changed without the code")
	}
}

func TestApplyConfirmed(t *testing.T) {
	e := newApplyEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.Plan(t, model.OriginCLI, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	code := strings.ToLower(strings.TrimPrefix(p.ID, "P-"))
	out, err := e.apply(t, strings.ToLower(p.ID), code+"\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"→ deletes ~/Library/Logs/A",
		`Type "` + strings.TrimPrefix(p.ID, "P-") + `" to apply 1 item(s), 4.1 KB (SAFE: 1): `,
		"[1/1] O-aaaaaa Old app logs … done",
		"Plan " + p.ID + " applied: 1 done · 0 skipped · 0 failed",
		"Expected: 4.1 KB",
		"macdoc log",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "To apply it:") {
		t.Error("apply repeated the apply hint")
	}
	if present(target) || e.status(t, p.ID) != store.PlanApplied {
		t.Fatal("not applied")
	}
	if out, err := e.apply(t, p.ID, code+"\n"); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("second apply: %v\n%s", err, out)
	}
}

func TestApplyRiskyAsksPerItem(t *testing.T) {
	e := newApplyEnv(t)
	safe := e.folder(t, "Library/Logs/A")
	venv := e.folder(t, "code/app/.venv")
	p := e.Plan(t, model.OriginCLI,
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, safe),
		e.item(t, "O-bbbbbb", "python.venv_stale", model.RiskRisky, venv))
	code := strings.TrimPrefix(p.ID, "P-")
	out, err := e.apply(t, p.ID, "n\n"+code+"\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "RISKY O-bbbbbb") || !strings.Contains(out, "[y/N]") || !strings.Contains(out, "(SAFE: 1)") {
		t.Errorf("no per-item prompt:\n%s", out)
	}
	if present(safe) || !present(venv) {
		t.Fatal("wrong items applied")
	}
	if !strings.Contains(out, "skipped: not approved") {
		t.Errorf("declined item not reported:\n%s", out)
	}
}

func TestApplyClaudePlanConfirmsEachItem(t *testing.T) {
	e := newApplyEnv(t)
	a := e.folder(t, "Library/Logs/A")
	b := e.folder(t, "Library/Logs/B")
	p := e.Plan(t, model.OriginClaude,
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, a),
		e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, b))
	out, err := e.apply(t, p.ID, "y\nn\n"+strings.TrimPrefix(p.ID, "P-")+"\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Claude proposed this plan "+cli.ProposedAt(p.CreatedAt)+".") || strings.Count(out, "[y/N]") != 2 {
		t.Errorf("no per-item confirmation:\n%s", out)
	}
	if present(a) || !present(b) {
		t.Fatal("wrong items applied")
	}
	// Declining everything changes nothing and leaves the plan pending.
	e2 := newApplyEnv(t)
	c := e2.folder(t, "Library/Logs/C")
	p2 := e2.Plan(t, model.OriginClaude, e2.item(t, "O-cccccc", "user.logs", model.RiskSafe, c))
	out, _ = e2.apply(t, p2.ID, "\n")
	if !strings.Contains(out, "Nothing approved") || !present(c) || e2.status(t, p2.ID) != store.PlanPending {
		t.Fatalf("declined plan changed something:\n%s", out)
	}
}

func TestApplyRefusesBadPlans(t *testing.T) {
	e := newApplyEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.Plan(t, model.OriginCLI, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if _, err := e.apply(t, "../../etc", ""); err == nil {
		t.Error("accepted a path")
	}
	// Edit the plan file: it no longer verifies.
	pf := filepath.Join(planner.Dir(store.DefaultPath(e.Home)), p.ID+".json")
	b, _ := os.ReadFile(pf)
	if err := os.WriteFile(pf, bytes.Replace(b, []byte("Library/Logs/A"), []byte("Library/Logs/B"), 1), 0o600); err != nil { //nolint:gosec // G703: a test fixture path.
		t.Fatal(err)
	}
	out, err := e.apply(t, p.ID, strings.TrimPrefix(p.ID, "P-")+"\n")
	if err == nil || !strings.Contains(out, "doesn't verify") || !present(target) {
		t.Fatalf("edited plan: %v\n%s", err, out)
	}
}

func TestQuarantineCommands(t *testing.T) {
	e := newApplyEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.Plan(t, model.OriginCLI, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if _, err := e.applyKeep(t, p.ID, strings.TrimPrefix(p.ID, "P-")+"\n"); err != nil {
		t.Fatal(err)
	}
	recs, _ := e.st.QuarantineEntries()
	qid := recs[0].ID
	g := &cli.GlobalFlags{}
	uh := cli.UserHome
	cli.UserHome = func() (string, error) { return e.Home, nil }
	t.Cleanup(func() { cli.UserHome = uh })

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := cli.RunQuarantineList(cmd, g, applyNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, qid) || !strings.Contains(s, "~/Library/Logs/A") || !strings.Contains(s, "held") {
		t.Errorf("list:\n%s", s)
	}
	out.Reset()
	if err := cli.RunQuarantineList(cmd, g, applyNow.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "expired") || !strings.Contains(s, "ready to purge") {
		t.Errorf("expired list:\n%s", s)
	}

	// Purge needs the word.
	out.Reset()
	cmd.SetIn(strings.NewReader("yes\n"))
	if err := cli.RunPurge(cmd, g, e.Home, []string{qid}, false, applyNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Not confirmed") || !present(filepath.Join(e.Home, "Library/Application Support/macdoc/quarantine", qid)) {
		t.Fatalf("purge without the word:\n%s", out.String())
	}
	if err := cli.RunPurge(cmd, g, e.Home, nil, false, applyNow); err == nil {
		t.Error("purge with nothing selected ran")
	}
	if err := cli.RunPurge(cmd, g, e.Home, []string{"Q-ZZZZ"}, false, applyNow); err == nil {
		t.Error("purge of an unknown entry ran")
	}

	// Restore puts it back.
	out.Reset()
	if err := cli.RunRestore(cmd, g, e.Home, strings.ToLower(qid), func() time.Time { return applyNow.Add(time.Hour) }); err != nil {
		t.Fatal(err)
	}
	if !present(filepath.Join(target, "f")) || !strings.Contains(out.String(), "Restored "+qid) {
		t.Fatalf("restore:\n%s", out.String())
	}

	// The log shows the whole story.
	out.Reset()
	res, _, err := audit.Read(audit.Path(store.DefaultPath(e.Home)), 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range res {
		names = append(names, r.Event)
	}
	if got := strings.Join(names, ","); got != "apply_started,item_done,apply_finished,quarantine_restored" {
		t.Errorf("audit = %s", got)
	}
}

func TestPurgeConfirmed(t *testing.T) {
	e := newApplyEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.Plan(t, model.OriginCLI, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if _, err := e.applyKeep(t, p.ID, strings.TrimPrefix(p.ID, "P-")+"\n"); err != nil {
		t.Fatal(err)
	}
	uh := cli.UserHome
	cli.UserHome = func() (string, error) { return e.Home, nil }
	t.Cleanup(func() { cli.UserHome = uh })
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("purge\n"))
	// Not expired yet: --expired finds nothing.
	if err := cli.RunPurge(cmd, &cli.GlobalFlags{}, e.Home, nil, true, applyNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nothing has expired yet") {
		t.Fatalf("early purge:\n%s", out.String())
	}
	out.Reset()
	cmd.SetIn(strings.NewReader("purge\n"))
	if err := cli.RunPurge(cmd, &cli.GlobalFlags{}, e.Home, nil, true, applyNow.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "can't be undone") || !strings.Contains(out.String(), "purged") {
		t.Fatalf("purge:\n%s", out.String())
	}
	recs, _ := e.st.QuarantineEntries()
	if recs[0].Status != store.QuarantinePurged || present(filepath.Join(e.Home, "Library/Application Support/macdoc/quarantine", recs[0].ID)) {
		t.Fatal("not purged")
	}
}

func TestRenderLog(t *testing.T) {
	n := int64(-2048)
	res := engine.AuditResult{Records: []audit.Record{
		{TS: applyNow, Event: audit.Planned, Plan: "P-7F3K", Origin: "cli", Items: 2, Expected: 5000},
		{TS: applyNow, Event: audit.Started, Plan: "P-7F3K", Items: 2},
		{TS: applyNow, Event: audit.Done, Item: "O-aaaaaa", Target: "/Users/me/Library/Logs/A", Entry: "Q-91XZ"},
		{TS: applyNow, Event: audit.Skipped, Item: "O-bbbbbb", Argv: []string{"brew", "cleanup"}, Reason: "brew isn't installed any more"},
		{TS: applyNow, Event: audit.Finished, Plan: "P-7F3K", Result: "partial", Actual: &n},
	}}
	var out bytes.Buffer
	if err := cli.RenderLog(&out, res, "/Users/me"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plan P-7F3K · 2 item(s) · 5.0 KB (cli)", "done O-aaaaaa ~/Library/Logs/A → Q-91XZ",
		"skipped O-bbbbbb brew cleanup · brew isn't installed any more", "apply finished P-7F3K · partial · free space -2.0 KB"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}
