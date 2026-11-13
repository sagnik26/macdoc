package tests

import (
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
)

func completeScan(t *testing.T, s *store.Store, at time.Time) string {
	t.Helper()
	id, err := s.BeginScan("/Users/test", "test", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishScan(id, model.ScanComplete, 0, 0, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInventoryRoundTrip(t *testing.T) {
	s := openTemp(t)
	id := completeScan(t, s, t0)
	inv := store.Inventory{
		Software: []store.SoftwareRow{
			{ID: "app:com.figma.Desktop", Name: "Figma", Kind: "app", Version: "124.3", BundleID: "com.figma.Desktop", Source: "apps", Installed: t0},
			{ID: "brew:node", Name: "node", Kind: "formula", Source: "brew"}, // unknown install time
		},
		Owners:  []store.OwnerRow{{SoftwareID: "app:com.figma.Desktop", Path: "/Users/test/Library/Caches/com.figma.Desktop", Bytes: 500, Confidence: 0.9, Evidence: "bundle id"}},
		Sources: []store.SourceRow{{Source: "apps", State: "ok"}, {Source: "npm", State: "absent", Reason: "npm is not installed"}},
	}
	if err := s.SaveInventory(id, inv); err != nil {
		t.Fatal(err)
	}
	got, err := s.Inventory(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Software) != 2 || len(got.Owners) != 1 || len(got.Sources) != 2 {
		t.Fatalf("got %+v", got)
	}
	var figma store.SoftwareRow
	for _, r := range got.Software {
		if r.ID == "app:com.figma.Desktop" {
			figma = r
		}
		if r.ID == "brew:node" && !r.Installed.IsZero() {
			t.Errorf("unknown install time came back as %v, want zero", r.Installed)
		}
	}
	if figma.Version != "124.3" || !figma.Installed.Equal(t0) {
		t.Errorf("figma = %+v", figma)
	}
	if got.Owners[0].Bytes != 500 || got.Owners[0].Confidence != 0.9 {
		t.Errorf("owner = %+v", got.Owners[0])
	}
}

func TestSaveInventoryReplacesTheSnapshot(t *testing.T) {
	s := openTemp(t)
	id := completeScan(t, s, t0)
	if err := s.SaveInventory(id, store.Inventory{Software: []store.SoftwareRow{{ID: "npm:a", Name: "a"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveInventory(id, store.Inventory{Software: []store.SoftwareRow{{ID: "npm:b", Name: "b"}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Inventory(id)
	if len(got.Software) != 1 || got.Software[0].ID != "npm:b" {
		t.Errorf("snapshot not replaced: %+v", got.Software)
	}
}

func TestScanWithoutSnapshotIsEmpty(t *testing.T) {
	s := openTemp(t)
	id := completeScan(t, s, t0)
	got, err := s.Inventory(id)
	if err != nil || len(got.Software)+len(got.Owners)+len(got.Sources) != 0 {
		t.Errorf("got %+v, %v; want an empty inventory", got, err)
	}
}

func TestPreviousScanIsTheOneBefore(t *testing.T) {
	s := openTemp(t)
	first := completeScan(t, s, t0)
	second := completeScan(t, s, t0.Add(time.Hour))
	prev, ok, err := s.PreviousScanID(second)
	if err != nil || !ok || prev != first {
		t.Errorf("previous of second = %q, %v, %v; want %q", prev, ok, err, first)
	}
	if _, ok, _ := s.PreviousScanID(first); ok {
		t.Error("the first scan has no previous scan")
	}
}

func TestInventoryGoesWithItsScan(t *testing.T) {
	s := openTemp(t)
	id := completeScan(t, s, t0)
	if err := s.SaveInventory(id, store.Inventory{Software: []store.SoftwareRow{{ID: "npm:a", Name: "a"}}}); err != nil {
		t.Fatal(err)
	}
	// Remove the scan row itself; the snapshot must cascade with it.
	if _, err := s.DB.Exec(`DELETE FROM scans WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Inventory(id)
	if len(got.Software) != 0 {
		t.Errorf("snapshot survived its scan: %+v", got.Software)
	}
}
