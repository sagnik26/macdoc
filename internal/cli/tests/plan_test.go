package tests

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

var planNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func samplePlan() engine.PlanResult {
	return engine.PlanResult{
		Status: planner.Pending,
		Plan: model.Plan{
			SchemaVersion: "1", ID: "P-7F3K", Origin: model.OriginCLI, ScanID: "S-TEST", Request: "--safe",
			CreatedAt: planNow, ExpiresAt: planNow.Add(30 * time.Minute), TotalBytes: 566_000_000,
			Items: []model.PlanItem{
				{OpportunityID: "O-brewaa", Title: "Homebrew old versions", ExpectedBytes: 400_000_000, BytesSource: model.SourceTool,
					Risk: model.RiskSafe, Method: model.MethodNative, Argv: []string{"brew", "cleanup", "--prune=all"},
					Restore: model.Restore{Kind: model.RestoreRedownload}},
				{OpportunityID: "O-logsaa", Title: "Old app logs · Zoom", ExpectedBytes: 6_000_000, BytesSource: model.SourceMeasured,
					Risk: model.RiskSafe, Method: model.MethodQuarantine, Target: "/Users/me/Library/Logs/Zoom",
					Restore: model.Restore{Kind: model.RestoreNone}},
				{OpportunityID: "O-nodeaa", Title: "Node.js v18.0.0", ExpectedBytes: 160_000_000, BytesSource: model.SourceMeasured,
					Risk: model.RiskCaution, RiskReason: "the project has uncommitted changes", Method: model.MethodQuarantine,
					Target: "/Users/me/.nvm/versions/node/v18.0.0", Preconditions: []string{"app_not_running:Xcode", "git_clean"},
					Restore: model.Restore{Kind: model.RestoreRedownload, Note: "Run nvm install 18."}},
				{OpportunityID: "O-modelb", Title: "llama3:8b", BytesSource: model.SourceUnknown,
					Risk: model.RiskCaution, Method: model.MethodNative, Argv: []string{"ollama", "rm", "llama3:8b"},
					Restore: model.Restore{Kind: model.RestoreRedownload}},
			},
		},
		Refused:  []planner.Refusal{{ID: "O-simsaa", Title: "Leftover runtimes", Reason: "advice only: macdoc takes no action on it"}},
		Snapshot: engine.SnapshotNote{Count: 2},
	}
}

func TestRenderPlan(t *testing.T) {
	var buf bytes.Buffer
	if err := cli.RenderPlan(&buf, samplePlan(), "/Users/me", planNow.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	want := `Plan P-7F3K · 4 item(s) · 566 MB · expires in 25 min · made by you (macdoc apply --safe)

 SAFE ─────────────────────────────────────────────────────────── 406 MB
  O-brewaa  Homebrew old versions                           400 MB
            → runs brew cleanup --prune=all
            Not undoable by macdoc: re-downloads on demand
  O-logsaa  Old app logs · Zoom                             6.0 MB
            → deletes ~/Library/Logs/Zoom
            Not undoable by macdoc: nothing to restore

 CAUTION ──────────────────────────────────────────────────────── 160 MB
  O-nodeaa  Node.js v18.0.0                                 160 MB
            → deletes ~/.nvm/versions/node/v18.0.0
            Not undoable by macdoc: re-downloads on demand. Run nvm install 18.
            Risk raised: the project has uncommitted changes
            Checks first: Xcode isn't running · the project has no uncommitted changes
  O-modelb  llama3:8b                                            —
            → runs ollama rm llama3:8b
            Not undoable by macdoc: re-downloads on demand

 Freed right away: 566 MB
 Plus 1 item(s) of unknown size.
 2 local Time Machine snapshot(s) exist: macOS may keep some of the freed space until they expire.

 Not included
  O-simsaa Leftover runtimes: advice only: macdoc takes no action on it

 Nothing has been changed.
`
	got := buf.String()
	if i := strings.Index(got, " To apply it"); i < 0 || !strings.HasPrefix(got[i:], " To apply it: macdoc apply P-7F3K · expires ") {
		t.Errorf("missing the apply line:\n%s", got)
	} else {
		got = got[:i]
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Done-when: the dry run matches exactly what the executor would do. The
// recording performer gets the same calls, in the same order, as the "→"
// lines of the dry run, and nothing else.
func TestDryRunParity(t *testing.T) {
	res := samplePlan()
	var buf bytes.Buffer
	if err := cli.RenderPlan(&buf, res, "/Users/me", planNow); err != nil {
		t.Fatal(err)
	}
	var shown []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "→ ") {
			shown = append(shown, l)
		}
	}
	rec := &recordingPerformer{Home: "/Users/me"}
	for _, r := range planner.Perform(res.Plan, rec) {
		if r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	if strings.Join(shown, "\n") != strings.Join(rec.calls, "\n") {
		t.Errorf("dry run shows:\n%s\n\nexecutor would do:\n%s", strings.Join(shown, "\n"), strings.Join(rec.calls, "\n"))
	}
	if len(rec.calls) != len(res.Plan.Items) {
		t.Errorf("%d actions for %d items", len(rec.calls), len(res.Plan.Items))
	}
}

// recordingPerformer records what an executor would be asked to do, in the
// dry run's words.
type recordingPerformer struct {
	Home  string
	calls []string
}

func (r *recordingPerformer) RunNative(_ model.PlanItem, argv []string) error {
	r.calls = append(r.calls, "→ runs "+strings.Join(argv, " "))
	return nil
}

func (r *recordingPerformer) Quarantine(_ model.PlanItem, target string) error {
	r.calls = append(r.calls, "→ deletes "+cli.TildePath(r.Home, target))
	return nil
}

func (r *recordingPerformer) System(_ model.PlanItem, target string) error {
	r.calls = append(r.calls, "→ deletes "+target+" (administrator rights)")
	return nil
}

func (r *recordingPerformer) Sweep(_ model.PlanItem, root string) error {
	r.calls = append(r.calls, "→ deletes .DS_Store files in "+cli.TildePath(r.Home, root))
	return nil
}

func TestRenderPlanStatuses(t *testing.T) {
	for st, want := range map[planner.Status]string{
		planner.Expired: "This plan has expired.",
		planner.Applied: "already applied",
		planner.Invalid: "doesn't verify and can't be applied",
	} {
		res := samplePlan()
		res.Status = st
		var buf bytes.Buffer
		_ = cli.RenderPlan(&buf, res, "/Users/me", planNow.Add(time.Hour))
		mustContain(t, buf.String(), want, "expired ·")
		if strings.Contains(buf.String(), "To apply it") {
			t.Errorf("%s: a plan that can't be applied must not offer the apply command", st)
		}
	}
	res := samplePlan()
	res.Plan.Origin = model.OriginClaude
	var buf bytes.Buffer
	_ = cli.RenderPlan(&buf, res, "/Users/me", planNow)
	mustContain(t, buf.String(), "proposed by Claude")
}

func TestPlanSelector(t *testing.T) {
	good := map[string]struct {
		args         []string
		safe         bool
		tier, recipe string
		want         string
	}{
		"ids":    {args: []string{"O-ABCDEF", "o-ghijkl"}, want: "O-abcdef O-ghijkl"},
		"safe":   {safe: true, want: "--safe"},
		"tier":   {tier: "Caution", want: "--tier caution"},
		"recipe": {recipe: "user.logs", want: "--recipe user.logs"},
	}
	for name, c := range good {
		sel, err := cli.PlanSelector(c.args, c.safe, c.tier, c.recipe)
		if err != nil || sel.Request() != c.want {
			t.Errorf("%s: %q, %v", name, sel.Request(), err)
		}
	}
	bad := map[string]struct {
		args         []string
		safe         bool
		tier, recipe string
		want         string
	}{
		"nothing": {want: "say what to apply"},
		"two":     {args: []string{"O-abcdef"}, safe: true, want: "use one of"},
		"path":    {args: []string{"/Users/me/Library/Caches"}, want: "never paths"},
		"advise":  {tier: "advise", want: "advice-only"},
		"tier":    {tier: "blocked", want: "unknown tier"},
	}
	for name, c := range bad {
		if _, err := cli.PlanSelector(c.args, c.safe, c.tier, c.recipe); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

// Dashboard: space marks, p seals a plan from the marked items, the dry run
// opens, and marks are cleared.
func TestTUIMarkAndPlan(t *testing.T) {
	cli.NowFunc = func() time.Time { return planNow }
	t.Cleanup(func() { cli.NowFunc = time.Now })
	var got planner.Selector
	m := step(t, loaded(t, 100, 40), keys("2")...)
	m.Deps.MakePlan = func(_ *cli.TuiDeps, sel planner.Selector, pre *engine.OpportunityResult) (engine.PlanResult, error) {
		got = sel
		if pre == nil || len(pre.Opportunities) == 0 {
			t.Error("the dashboard should pass its opportunities")
		}
		return samplePlan(), nil
	}
	ops := m.Items()
	clear(m.Marked) // the picker pre-marks SAFE items; this test marks by hand

	m = step(t, m, keys(" ")...) // marks item 1, moves to 2
	m = step(t, m, keys("down", " ")...)
	if len(m.Marked) != 2 || !m.Marked[ops[0].ID] || !m.Marked[ops[2].ID] || m.Cursor != 3 {
		t.Fatalf("marked %v cursor %d", m.Marked, m.Cursor)
	}
	mustContain(t, m.View(), "✓ 2 marked", "enter remove", "│ ✓ │", "space mark")

	m = step(t, m, keys("up", " ")...) // unmark item 3
	if len(m.Marked) != 1 {
		t.Fatalf("unmark: %v", m.Marked)
	}
	m = step(t, m, keys("up", "up", " ")...)
	m = step(t, m, keys("p")...)
	if !m.Planning {
		t.Fatal("p should start sealing a plan")
	}
	m = runPlan(t, m)
	if strings.Join(got.IDs, " ") != ops[0].ID+" "+ops[1].ID || m.Overlay != cli.OverlayPlan || len(m.Marked) != 0 {
		t.Errorf("selector %v overlay %d marked %v", got.IDs, m.Overlay, m.Marked)
	}
	mustContain(t, m.View(), "Plan P-7F3K", "→ runs brew cleanup --prune=all", "Nothing has been changed", "Plan P-7F3K sealed")
	m = step(t, m, keys("esc")...)
	if m.Overlay != cli.OverlayNone {
		t.Error("esc should close the plan")
	}
}

func runPlan(t *testing.T, m cli.TuiModel) cli.TuiModel {
	t.Helper()
	sel := m.MarkedSelector()
	if sel == nil {
		t.Fatal("no selector")
	}
	// What startPlan's command does: plan from the engine's data (planOps),
	// never the cleaned display copy.
	res, err := m.Deps.MakePlan(m.Deps, *sel, m.Data.PlanOps)
	return step(t, m, cli.PlanMsg{Res: res, Err: err})
}

func TestTUIPlanEdgeCases(t *testing.T) {
	m := step(t, loaded(t, 100, 40), keys("2")...)
	var sel planner.Selector
	m.Deps.MakePlan = func(_ *cli.TuiDeps, s planner.Selector, _ *engine.OpportunityResult) (engine.PlanResult, error) {
		sel = s
		return engine.PlanResult{Refused: []planner.Refusal{{ID: "O-x", Reason: "it no longer exists"}}}, planner.ErrNothing
	}
	ops := m.Items()
	// Advice-only items can't be marked.
	for i, op := range ops {
		if op.Risk == model.RiskAdvise {
			m.Cursor = i
			m = step(t, m, keys(" ")...)
			mustContain(t, m.View(), "Advice-only items can't go into a plan")
			if m.Marked[op.ID] {
				t.Error("an advice item was marked")
			}
		}
	}
	// No marks: p plans the selected item.
	m.Cursor = 0
	m = step(t, m, keys("p")...)
	if !m.Planning {
		t.Fatal("p with nothing marked should plan the selected item")
	}
	m = step(t, m, cmdMsg(t, m, ops[0].ID))
	if !strings.Contains(m.View(), "Nothing to plan: O-x: it no longer exists") || m.Planning || strings.Join(sel.IDs, "") != ops[0].ID {
		t.Errorf("refusal flash missing or wrong selector %v:\n%s", sel.IDs, m.View())
	}

	// /plan safe plans the whole tier; other tabs explain where plans are made.
	m = step(t, m, keys("/")...)
	m = step(t, m, keys("plan safe", "enter")...)
	if !m.Planning {
		t.Error("/plan safe should start a plan")
	}
	m.Planning = false
	m = step(t, m, keys("4", "p")...)
	mustContain(t, m.View(), "Plans are made on the Suggestions tab")
	m = step(t, m, keys("/")...)
	m = step(t, m, keys("plan everything", "enter")...)
	mustContain(t, m.View(), "Usage: /plan")
}

// cmdMsg runs the dashboard's plan function the way the command would.
func cmdMsg(t *testing.T, m cli.TuiModel, id string) tea.Msg {
	t.Helper()
	res, err := m.Deps.MakePlan(m.Deps, planner.Selector{IDs: []string{id}}, m.Data.PlanOps)
	if err == nil {
		err = errors.New("expected a refusal")
	}
	return cli.PlanMsg{Res: res, Err: err}
}
