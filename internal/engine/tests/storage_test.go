package tests

import (
	"context"
	"errors"
	"macdoc/internal/engine"
	"strings"
	"testing"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/recipes"
)

const (
	fakeDiskInfo = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>APFSContainerReference</key><string>disk3</string>
<key>APFSContainerSize</key><integer>245000000000</integer>
<key>APFSContainerFree</key><integer>9000000000</integer>
</dict></plist>`
	fakeAPFSList = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>Containers</key><array><dict>
<key>ContainerReference</key><string>disk3</string>
<key>Volumes</key><array>
<dict><key>Name</key><string>Data</string><key>Roles</key><array><string>Data</string></array><key>CapacityInUse</key><integer>200000000000</integer></dict>
<dict><key>Name</key><string>VM</string><key>Roles</key><array><string>VM</string></array><key>CapacityInUse</key><integer>3000000000</integer></dict>
</array></dict></array></dict></plist>`
	fakeSnapshots = "Snapshots for disk /:\ncom.apple.TimeMachine.2026-10-27-101500.local\ncom.apple.TimeMachine.2026-10-27-111500.local\n"
)

func fakeSystemRunner() *platform.FakeRunner {
	return &platform.FakeRunner{Responses: map[string]platform.Result{
		"diskutil info -plist /":      {Stdout: []byte(fakeDiskInfo)},
		"diskutil apfs list -plist":   {Stdout: []byte(fakeAPFSList)},
		"tmutil listlocalsnapshots /": {Stdout: []byte(fakeSnapshots)},
	}}
}

func part(t *testing.T, parts []engine.SystemDataPart, prefix string) engine.SystemDataPart {
	t.Helper()
	for _, p := range parts {
		if strings.HasPrefix(p.Name, prefix) {
			return p
		}
	}
	t.Fatalf("no part %q in %+v", prefix, parts)
	return engine.SystemDataPart{}
}

func TestStorage(t *testing.T) {
	f := newFixture(t, time.Hour)
	f = withNodes(t, f,
		model.Node{Path: "/", Kind: model.NodeDir, Bytes: 150 * gb, Depth: 0},
		model.Node{Path: "/private", Kind: model.NodeDir, Bytes: 6 * gb, Depth: 1},
		model.Node{Path: "/private/var", Kind: model.NodeDir, Bytes: 6 * gb, Depth: 2},
		model.Node{Path: "/private/var/folders", Kind: model.NodeDir, Bytes: 5 * gb, Depth: 3},
		model.Node{Path: f.p("Library/Caches/big"), Kind: model.NodeDir, Bytes: 4 * gb, Depth: 8},
		model.Node{Path: f.p("Library/Caches/big/inner"), Kind: model.NodeDir, Bytes: 1500 * 1_000_000, Depth: 9},
		model.Node{Path: f.p("Movies/raw.mov"), Kind: model.NodeFile, Bytes: 3 * gb, Depth: 8},
		model.Node{Path: f.p("VMs"), Kind: model.NodeDir, Bytes: 2 * gb, Depth: 7},
	)
	q := rec("t.q", model.RiskSafe, model.MethodQuarantine)
	n := rec("t.n", model.RiskCaution, model.MethodNative)
	n.Category = "containers"
	adv := rec("t.adv", model.RiskAdvise, model.MethodAdvise)
	adv.Category = "containers"
	setFindings(t, map[string][]recipes.Finding{
		"t.q":   {{Target: f.p("Library/Caches/big"), Bytes: measured(4 * gb)}},
		"t.n":   {{Bytes: measured(2 * gb)}, {Target: "/x", Bytes: model.Bytes{Source: model.SourceUnknown}}},
		"t.adv": {{Target: f.p("VMs"), Bytes: measured(2 * gb)}},
	})
	o := f.opts(q, n, adv)
	o.Runner = fakeSystemRunner()
	res, err := engine.Storage(context.Background(), engine.StorageOptions{OpportunityOptions: o, Large: true})
	if err != nil {
		t.Fatal(err)
	}

	// Categories.
	if len(res.Categories) != 2 {
		t.Fatalf("categories = %+v", res.Categories)
	}
	c := res.Categories[0]
	if c.Category != "containers" || c.Bytes != 4*gb || c.Count != 3 || c.UnknownCount != 1 ||
		c.ByRisk[model.RiskCaution] != 2*gb || c.ByRisk[model.RiskAdvise] != 2*gb {
		t.Errorf("containers = %+v", c)
	}
	if res.Categories[1].Category != "package-caches" || res.Categories[1].ByRisk[model.RiskSafe] != 4*gb {
		t.Errorf("package-caches = %+v", res.Categories[1])
	}

	// System Data.
	if p := part(t, res.SystemData, "Local Time Machine snapshots"); p.Name != "Local Time Machine snapshots (2)" || p.Bytes.Source != model.SourceUnknown || p.Bytes.Reason == "" {
		t.Errorf("snapshots = %+v", p)
	}
	if p := part(t, res.SystemData, "Swap"); p.Bytes != (model.Bytes{Value: 3 * gb, Source: model.SourceTool}) {
		t.Errorf("vm = %+v", p)
	}
	if p := part(t, res.SystemData, "Not visible"); p.Bytes.Value != 50*gb || p.Bytes.Source != model.SourceEstimated {
		t.Errorf("hidden = %+v", p)
	}
	if p := part(t, res.SystemData, "Purgeable"); p.Bytes.Source == "" || (p.Bytes.Source == model.SourceUnknown && p.Bytes.Reason == "") {
		t.Errorf("purgeable = %+v", p)
	}
	if p := part(t, res.SystemData, "Temporary"); p.Bytes != (model.Bytes{Value: 5 * gb, Source: model.SourceMeasured}) {
		t.Errorf("var/folders = %+v", p)
	}
	if p := part(t, res.SystemData, "Shared simulator"); p.Bytes.Source != model.SourceUnknown || p.Bytes.Reason != "not in the latest scan" {
		t.Errorf("CoreSimulator = %+v", p)
	}
	for _, p := range res.SystemData {
		if p.Explain == "" {
			t.Errorf("%s has no explanation", p.Name)
		}
	}

	// --large: the cache (an opportunity target) and its child are excluded,
	// as is the advise target; the movie file is not covered by any recipe.
	if len(res.Large) != 1 || res.Large[0].Path != f.p("Movies/raw.mov") {
		t.Errorf("large = %v", paths(res.Large))
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

func TestStorageUnknownsWithoutRunner(t *testing.T) {
	f := newFixture(t, time.Hour)
	res, err := engine.Storage(context.Background(), engine.StorageOptions{OpportunityOptions: f.opts()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Large != nil || len(res.Categories) != 0 {
		t.Errorf("res = %+v", res)
	}
	for _, p := range res.SystemData {
		if p.Bytes.Source != model.SourceUnknown || p.Bytes.Value != 0 || p.Bytes.Reason == "" {
			t.Errorf("%s: %+v, want unknown with a reason", p.Name, p.Bytes)
		}
	}
	if len(res.Warnings) != 2 {
		t.Errorf("warnings = %q", res.Warnings)
	}

	// A failing runner: same unknowns, with the errors as warnings.
	o := f.opts()
	o.Runner = &platform.FakeRunner{Errors: map[string]error{"tmutil listlocalsnapshots /": errors.New("denied")}}
	res, err = engine.Storage(context.Background(), engine.StorageOptions{OpportunityOptions: o})
	if err != nil {
		t.Fatal(err)
	}
	if p := part(t, res.SystemData, "Local Time Machine"); p.Bytes.Reason != "snapshot list unavailable" {
		t.Errorf("snapshots = %+v", p)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "denied") {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

func TestStorageNoScan(t *testing.T) {
	if _, err := engine.Storage(context.Background(), engine.StorageOptions{OpportunityOptions: engine.OpportunityOptions{Home: t.TempDir(), Catalog: &recipes.Catalog{}}}); !errors.Is(err, engine.ErrNoScan) {
		t.Fatalf("err = %v", err)
	}
}
