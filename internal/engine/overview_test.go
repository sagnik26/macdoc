package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/recipes"
	"macdoc/internal/store"
	"macdoc/internal/testutil/fstree"
)

func overviewHome(t *testing.T) (home, root string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "root")
	fstree.Build(t, root, fstree.File("Users/a/big.bin", 2<<20), fstree.File("Library/x.bin", 1<<20))
	home = filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home, root
}

func TestOverviewScansWhenNoScanThenReuses(t *testing.T) {
	home, root := overviewHome(t)
	o := OverviewOptions{Home: home, Runner: &platform.FakeRunner{}, ScanRoot: root, Catalog: &recipes.Catalog{}}

	first, err := Overview(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Scanned || first.Scan.Status != model.ScanComplete || len(first.TopLevel) == 0 {
		t.Fatalf("first overview = %+v", first)
	}
	if len(first.Health.Report.Warnings) == 0 {
		t.Error("health warnings expected with a failing runner")
	}

	second, err := Overview(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanned || second.Scan.ID != first.Scan.ID || second.ScanStale {
		t.Fatalf("second overview should reuse scan %s: %+v", first.Scan.ID, second.Scan)
	}
	if len(second.TopLevel) != len(first.TopLevel) {
		t.Errorf("top level from store = %v, from scan = %v", paths(second.TopLevel), paths(first.TopLevel))
	}

	o.Rescan = true
	third, err := Overview(context.Background(), o)
	if err != nil || !third.Scanned || third.Scan.ID == first.Scan.ID {
		t.Fatalf("rescan = %+v, %v", third.Scan, err)
	}

	o.Rescan, o.Now = false, time.Now().Add(48*time.Hour)
	stale, err := Overview(context.Background(), o)
	if err != nil || !stale.ScanStale {
		t.Fatalf("expected stale scan, got %+v, %v", stale, err)
	}
}

func TestOverviewErrors(t *testing.T) {
	home, _ := overviewHome(t)
	// The store path is a directory → open fails.
	if err := os.MkdirAll(store.DefaultPath(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Overview(context.Background(), OverviewOptions{Home: home, Runner: &platform.FakeRunner{}, Catalog: &recipes.Catalog{}}); err == nil {
		t.Error("expected an error when the store can't open")
	}
	// Scan root missing → the scan fails.
	home2, _ := overviewHome(t)
	if _, err := Overview(context.Background(), OverviewOptions{Home: home2, Runner: &platform.FakeRunner{}, ScanRoot: filepath.Join(home2, "nope"), Catalog: &recipes.Catalog{}}); err == nil {
		t.Error("expected an error when the scan root is missing")
	}
}

func TestStoragePrecomputed(t *testing.T) {
	home, root := overviewHome(t)
	o := OverviewOptions{Home: home, Runner: &platform.FakeRunner{}, ScanRoot: root, Catalog: &recipes.Catalog{}}
	ov, err := Overview(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	pre := ov.Opportunities
	pre.Opportunities = []model.Opportunity{{ID: "O-test01", Category: "logs", Risk: model.RiskSafe, Bytes: model.Bytes{Value: 5, Source: model.SourceMeasured}}}
	res, err := Storage(context.Background(), StorageOptions{
		OpportunityOptions: OpportunityOptions{Home: home, Runner: &platform.FakeRunner{}, Catalog: &recipes.Catalog{}},
		Precomputed:        &pre,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Categories) != 1 || res.Categories[0].Category != "logs" {
		t.Errorf("precomputed opportunities not used: %+v", res.Categories)
	}
}
