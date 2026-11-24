package tests

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"

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
	want := `Plan P-7F3K · 4 item(s) · 566 MB · expires in 25 min · made by you (--safe)

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
	if strings.Contains(got, "macdoc apply") {
		t.Errorf("the plan points at a command that no longer exists:\n%s", got)
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

// Dashboard: in /clean, space ticks rows (several can be deleted together with enter),
// ticks again to untick, and a ticks all. Advice-only items are not in the list.
func TestTUICleanTicks(t *testing.T) {
	m := loaded(t, 100, 40)
	next, _ := m.RunCommand("/clean")
	m = next.(cli.TuiModel)
	items := 0
	for _, op := range m.Items() {
		if op.Method != model.MethodAdvise && op.Risk != model.RiskAdvise && op.Risk != model.RiskBlocked {
			items++
		}
	}
	if len(m.Marked) != 0 {
		t.Fatalf("nothing is ticked at the start: %v", m.Marked)
	}
	mustContain(t, m.Screen(), "Choose what to delete", "nothing ticked yet", "space ticks")

	m = step(t, m, keys(" ")...) // ticks row 1, moves to 2
	m = step(t, m, keys("down", " ")...)
	if len(m.Marked) != 2 {
		t.Fatalf("two ticked: %v", m.Marked)
	}
	mustContain(t, m.Screen(), "2 ticked", "✓")

	m = step(t, m, keys("up", " ")...) // unticks row 3
	if len(m.Marked) != 1 {
		t.Fatalf("unticking: %v", m.Marked)
	}
	m = step(t, m, keys("a")...)
	if len(m.Marked) != items {
		t.Errorf("a should tick all %d, got %d", items, len(m.Marked))
	}
	m = step(t, m, keys("a")...)
	if len(m.Marked) != 0 {
		t.Errorf("a again should untick all: %v", m.Marked)
	}
	for _, op := range m.Items() {
		if (op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise) && strings.Contains(m.Screen(), op.Title) {
			t.Errorf("the advice item %q must not be listed", op.Title)
		}
	}
}

// Enter with nothing ticked deletes nothing: it never deletes the highlighted row.
func TestTUICleanEnterWithNothingTicked(t *testing.T) {
	m := loaded(t, 100, 40)
	planned := 0
	m.Deps.MakePlan = func(*cli.TuiDeps, planner.Selector, *engine.OpportunityResult) (engine.PlanResult, error) {
		planned++
		return samplePlan(), nil
	}
	next, _ := m.RunCommand("/clean")
	m = step(t, next.(cli.TuiModel), keys("enter")...)
	if planned != 0 || m.Overlay == cli.OverlayRemove {
		t.Fatalf("nothing ticked: planned %d, overlay %d", planned, m.Overlay)
	}
	mustContain(t, m.Flash, "Tick something first")
}

// Without a scan, /clean says so.
func TestTUICleanWithoutAScan(t *testing.T) {
	m, _ := newIntroTUI(t, 100, 40, cli.IntroStatus{})
	next, _ := m.RunCommand("/clean")
	got := next.(cli.TuiModel)
	if !got.Loading && !strings.Contains(got.Flash, "No scan yet") {
		t.Errorf("/clean with no scan should load one or say so: loading=%v flash=%q", got.Loading, got.Flash)
	}
}
