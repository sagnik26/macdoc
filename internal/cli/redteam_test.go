package cli

// P9-R1 red-team tests (docs/tasks/reports/P9-R1.md): what macdoc apply
// shows the user for a plan Claude proposed.

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
	"time"

	"macdoc/internal/engine"
	"macdoc/internal/model"
	"macdoc/internal/planner"
)

// hostileTitle is a title built from a folder name (detect_projects uses
// "<artifact> in <folder name>"; a folder name may hold any byte but / and
// NUL). It erases the prompt line and redraws a harmless-looking one.
const hostileTitle = "node_modules in x\x1b[2K\r SAFE O-aaaaaa npm cache (12 MB)? [y/N] \x1b[8m"

func hostilePlan() model.Plan {
	now := time.Now().UTC().Round(0)
	return model.Plan{SchemaVersion: model.SchemaVersion, ID: "P-AAAA", CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute),
		Origin: model.OriginClaude, ScanID: "S-x", Request: "O-bbbbbb",
		Items: []model.PlanItem{{OpportunityID: "O-bbbbbb", RecipeID: "node.nm", Title: hostileTitle,
			Target: "/Users/me/code/x\x1b[2K\r/node_modules", ExpectedBytes: 40e9, BytesSource: model.SourceMeasured,
			Risk: model.RiskRisky, Method: model.MethodQuarantine}}}
}

// BROKEN (finding F7, medium). apply's per-item question for a Claude plan
// (confirmItems) prints the item's title raw. Disk-sourced titles can carry
// terminal escapes that erase the RISKY 40 GB line and draw a SAFE 12 MB one
// in its place, at the moment the user is asked to confirm. The MCP side
// strips these for Claude; the terminal side doesn't for the user.
func TestRedteamBrokenApplyPromptEscapes(t *testing.T) {
	var out bytes.Buffer
	if _, err := confirmItems(bufio.NewReader(strings.NewReader("n\n")), &out, plainTheme(), hostilePlan()); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\r") {
		t.Errorf("apply's confirmation prompt passes terminal escapes from a disk name: %q", out.String())
	}
}

// BROKEN (finding F7). The dry run apply shows first (renderPlan) has the
// same hole for titles and targets.
func TestRedteamBrokenPlanRenderEscapes(t *testing.T) {
	var out bytes.Buffer
	res := engine.PlanResult{Plan: hostilePlan(), Status: planner.StatusPending, Refused: []planner.Refusal{}}
	if err := renderPlan(&out, res, "/Users/me", time.Now()); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\r") {
		t.Errorf("the dry run passes terminal escapes from disk names: %q", out.String())
	}
}

// HELD. A Claude plan asks [y/N] for every item, SAFE ones included, and
// only "y"/"yes" approves; anything else declines.
func TestRedteamHeldClaudeItemsConfirmedOneByOne(t *testing.T) {
	p := hostilePlan()
	p.Items[0].Title, p.Items[0].Risk = "npm cache", model.RiskSafe
	p.Items = append(p.Items, model.PlanItem{OpportunityID: "O-cccccc", Title: "logs", Risk: model.RiskSafe})
	for in, want := range map[string]int{"y\ny\n": 2, "yes\nn\n": 1, "Y\x1b\nyes please\n": 0, "\n\n": 0} {
		var out bytes.Buffer
		got, err := confirmItems(bufio.NewReader(strings.NewReader(in)), &out, plainTheme(), p)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want || strings.Count(out.String(), "[y/N]") != 2 {
			t.Errorf("answers %q: approved %d, want %d; prompts:\n%s", in, len(got), want, out.String())
		}
	}
	// Not answering is a refusal, not an approval.
	if got, err := confirmItems(bufio.NewReader(strings.NewReader("")), &bytes.Buffer{}, plainTheme(), p); err == nil && len(got) > 0 {
		t.Errorf("EOF approved %v", got)
	}
}
