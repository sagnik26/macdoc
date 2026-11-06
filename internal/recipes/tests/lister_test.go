package tests

import (
	"context"
	"slices"
	"testing"

	"github.com/sagnik26/macdoc/internal/recipes"

	"github.com/sagnik26/macdoc/internal/platform"
)

// P6-R1 F8: re-checking a native item must not need scan data. Each lister
// names what the tool lists now, without recipes.Env.Nodes.

func TestAVDNamesListerNeedsNoScan(t *testing.T) {
	home, _ := avdFixture(t)
	withTools(t, "avdmanager")
	l, ok := recipes.LookupLister("android_avds")
	if !ok {
		t.Fatal("no lister for android_avds")
	}
	names, err := l(context.Background(), recipes.Env{Home: home}, recipes.Recipe{}) // Nodes deliberately nil
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names, "Pixel_7") || !slices.Contains(names, "Fresh") {
		t.Errorf("names = %v, want Pixel_7 and Fresh", names)
	}
	// Look-alikes and an AVD outside the AVD home are not listed.
	if slices.Contains(names, "Pixel_7x") || slices.Contains(names, "Custom") || slices.Contains(names, "bad name") {
		t.Errorf("names = %v, want no look-alike, outside-home or invalid names", names)
	}
}

func TestAVDNamesListerToolMissing(t *testing.T) {
	home, _ := avdFixture(t)
	withTools(t)
	l, _ := recipes.LookupLister("android_avds")
	names, err := l(context.Background(), recipes.Env{Home: home}, recipes.Recipe{})
	if err != nil || names != nil {
		t.Errorf("avdmanager missing: names = %v, err = %v; want none", names, err)
	}
}

func TestSimctlListerNeedsNoScan(t *testing.T) {
	withTools(t, "xcrun")
	fr := &platform.FakeRunner{Responses: map[string]platform.Result{
		keyDevs: {Stdout: readFixture(t, "w1_simctl_devices.json")},
	}}
	l, ok := recipes.LookupLister("simctl_idle_devices")
	if !ok {
		t.Fatal("no lister for simctl_idle_devices")
	}
	ids, err := l(context.Background(), recipes.Env{Runner: fr}, recipes.Recipe{}) // Nodes deliberately nil
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1A1B2C3D-0000-4000-8000-000000000003", "1A1B2C3D-0000-4000-8000-000000000004"}
	slices.Sort(ids)
	if !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want the two available shut-down devices", ids)
	}
	// Unavailable and booted devices, and a name that isn't a UDID, are not listed.
	for _, bad := range []string{
		"0A1B2C3D-0000-4000-8000-000000000001", // unavailable
		"1A1B2C3D-0000-4000-8000-000000000005", // booted
		"not a udid; rm Bogus",
	} {
		if slices.Contains(ids, bad) {
			t.Errorf("ids include %q", bad)
		}
	}
}

func TestSimctlListerToolMissing(t *testing.T) {
	withTools(t)
	l, _ := recipes.LookupLister("simctl_idle_devices")
	ids, err := l(context.Background(), recipes.Env{Runner: &platform.FakeRunner{}}, recipes.Recipe{})
	if err != nil || ids != nil {
		t.Errorf("xcrun missing: ids = %v, err = %v; want none", ids, err)
	}
}
