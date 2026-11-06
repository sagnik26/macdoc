package tests

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"

	"github.com/sagnik26/macdoc/internal/model"
)

func TestExplainCommand(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "wins", "--all")
	if err != nil {
		t.Fatal(err)
	}
	ids := regexp.MustCompile(`O-[a-z2-7]{6}`).FindAllString(out, -1)
	if len(ids) != 7 {
		t.Fatalf("ids = %v\n%s", ids, out)
	}
	for _, id := range ids {
		out, err := run(t, "explain", id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		mustContain(t, out, id+" · ", "Size", "Risk", "Restore", "What it is", "Nothing has been changed.")
	}

	// Upper case and without the prefix work too; JSON carries the base risk.
	out, err = run(t, "explain", strings.ToUpper(ids[0][2:]), "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string            `json:"command"`
		Data    cli.ExplainResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "explain" || env.Data.Opportunity.ID != ids[0] || env.Data.BaseRisk == "" {
		t.Errorf("envelope = %+v", env)
	}

	_, err = run(t, "explain", "O-zzzzzz")
	if err == nil || err.Error() != `O-zzzzzz not found in the latest scan; IDs change after a new scan` {
		t.Errorf("err = %v", err)
	}
}

func TestRenderExplain(t *testing.T) {
	cases := []struct {
		ex   cli.ExplainResult
		want []string
	}{
		{cli.ExplainResult{BaseRisk: model.RiskSafe, Opportunity: model.Opportunity{ID: "O-aaaaaa", Title: "npm cache", Target: "/Users/me/.npm/_cacache",
			Bytes: model.Bytes{Value: 3 * gb, Source: model.SourceTool, Reason: "npm reported"}, Risk: model.RiskCaution, Method: model.MethodNative, IdleDays: 2,
			Command: []string{"npm", "cache", "clean", "--force"}, DryRun: []string{"npm", "cache", "ls"},
			Restore: model.Restore{Kind: model.RestoreRedownload, RedownloadBytes: gb, Note: "npm downloads again."}, Confidence: 0.9,
			Explain: "npm keeps packages.", Note: "Corepack shim", Category: "package-caches", Toolchain: "node"}},
			[]string{"O-aaaaaa · npm cache", "Target      /Users/me/.npm/_cacache", "3.0 GB (tool): npm reported",
				"CAUTION (raised from SAFE: it changed in the last 7 days)", "2 days since last change",
				"the tool's own command: npm cache clean --force", "preview with: npm cache ls",
				"re-download ~1.0 GB. npm downloads again.", "package-caches · node", "90%", "What it is\n  npm keeps packages.", "Note\n  Corepack shim"}},
		{cli.ExplainResult{Opportunity: model.Opportunity{ID: "O-bbbbbb", Title: "DerivedData", Risk: model.RiskSafe, Method: model.MethodQuarantine, IdleDays: -1,
			Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "no permission"}, Restore: model.Restore{Kind: model.RestoreRebuild}}},
			[]string{"—  unknown (no permission)", "Idle        unknown", "deleted by macdoc after you confirm (checked again first)", "rebuilds on next build"}},
		{cli.ExplainResult{Opportunity: model.Opportunity{ID: "O-cccccc", Title: "Backup", Risk: model.RiskAdvise, Method: model.MethodAdvise,
			Bytes: model.Bytes{Source: model.SourceUnknown}, Restore: model.Restore{Kind: model.RestoreIrreplaceable},
			Steps: []string{"Open Finder.", "Run `xcrun simctl runtime delete all`"}}},
			[]string{"—  unknown\n", "advice only; macdoc takes no action", "can't be restored",
				"How to free it yourself\n  1. Open Finder.\n  2. Run xcrun simctl runtime delete all\n"}},
		{cli.ExplainResult{Opportunity: model.Opportunity{ID: "O-dddddd", Title: "x", Method: model.MethodQuarantine, Restore: model.Restore{Kind: model.RestoreNone},
			Bytes: model.Bytes{Value: 5, Source: model.SourceMeasured}}},
			[]string{"5 B (measured)", "nothing to restore"}},
	}
	for _, c := range cases {
		var b strings.Builder
		if err := cli.RenderExplain(&b, c.ex); err != nil {
			t.Fatal(err)
		}
		mustContain(t, b.String(), c.want...)
		if strings.Contains(b.String(), "0 B") {
			t.Errorf("unknown shown as zero:\n%s", b.String())
		}
	}
}
