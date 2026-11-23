package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/model"
)

// --yes with a tier cleans that tier and only that tier, with no terminal and
// no prompt, and the audit log says no person confirmed it.
func TestReclaimYesCleansOneTier(t *testing.T) {
	home, safe, caution, risky := reclaimFixture(t)
	termOn(t, false)
	out, err := runWithInput(t, "", "reclaim", "--tier", "safe", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "--yes: cleaning 1 item", "applied: 1 done")
	if present(safe) || !present(caution) || !present(risky) {
		t.Errorf("safe %v caution %v risky %v present", present(safe), present(caution), present(risky))
	}
	log, err := os.ReadFile(filepath.Join(home, "Library", "Application Support", "macdoc", "audit.jsonl"))
	if err != nil || !strings.Contains(string(log), `"non_interactive":true`) {
		t.Errorf("the audit log doesn't say the run was non-interactive: %v\n%s", err, log)
	}
	out, err = runWithInput(t, "", "reclaim", "--tier", "caution", "--yes")
	if err != nil || present(caution) || !present(risky) {
		t.Errorf("caution: %v present caution %v risky %v\n%s", err, present(caution), present(risky), out)
	}
}

// A bare --yes, --yes with a tier reclaim never cleans, and --dry-run with
// --yes all change nothing.
func TestReclaimYesRefusals(t *testing.T) {
	_, safe, caution, risky := reclaimFixture(t)
	termOn(t, false)
	for _, args := range [][]string{
		{"reclaim", "--yes"},
		{"reclaim", "--tier", "risky", "--yes"},
		{"reclaim", "--tier", "advise", "--yes"},
	} {
		if out, err := runWithInput(t, "", args...); err == nil {
			t.Errorf("%v accepted:\n%s", args, out)
		}
	}
	if _, err := runWithInput(t, "", "reclaim", "--tier", "safe", "--yes", "--dry-run"); err != nil {
		t.Errorf("--dry-run: %v", err)
	}
	if !present(safe) || !present(caution) || !present(risky) {
		t.Error("something was removed")
	}
}

// apply has no --yes.
func TestApplyHasNoYesFlag(t *testing.T) {
	reclaimFixture(t)
	termOn(t, false)
	if _, err := runWithInput(t, "", "apply", "--yes", "--safe"); err == nil {
		t.Error("apply accepted --yes")
	}
}

// The dry run says a system removal needs administrator rights and can't be
// undone, and names the file it deletes.
func TestRenderPlanShowsSystemRemoval(t *testing.T) {
	res := samplePlan()
	res.Plan.Items = []model.PlanItem{{OpportunityID: "O-sysaaa", Title: "System cache · Acme", ExpectedBytes: 4096, BytesSource: model.SourceMeasured,
		Risk: model.RiskCaution, Method: model.MethodSystem, Privileged: true, Target: "/Library/Caches/Acme",
		Restore: model.Restore{Kind: model.RestoreNone, Note: "Gone for good."}}}
	var b strings.Builder
	if err := cli.RenderPlan(&b, res, "/Users/me", planNow); err != nil {
		t.Fatal(err)
	}
	mustContain(t, b.String(), "→ deletes", "/Library/Caches/Acme (administrator rights)", "Permanent", "Needs administrator rights")
}
