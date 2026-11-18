package tests

import (
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
)

const containers = w2Home + "/Library/Containers"

// Only <bundle id>/Data/Library/Caches of a sandboxed app is listed.
func TestContainerCaches(t *testing.T) {
	nodes := fakeNodes{
		{Path: containers + "/com.vendor.app", Bytes: 5 << 30},
		{Path: containers + "/com.vendor.app/Data", Bytes: 5 << 30},
		{Path: containers + "/com.vendor.app/Data/Library/Caches", Bytes: 300 << 20, MtimeMax: w2DaysAgo(1)},
		{Path: containers + "/Other.App/data/library/caches", Bytes: 200 << 20}, // case differs, still the cache folder
		// Look-alikes and the rest of a container: never listed.
		{Path: containers + "/com.vendor.app/Data/Library/Application Support", Bytes: 4 << 30},
		{Path: containers + "/com.vendor.app/Data/Documents", Bytes: 1 << 30},
		{Path: containers + "/com.vendor.app/Data/Library/Caches/sub", Bytes: 200 << 20}, // deeper
		{Path: containers + "/com.vendor.app/Data/Library/Caches-old", Bytes: 200 << 20},
		{Path: containers + "/com.vendor.app/Library/Caches", Bytes: 200 << 20}, // not under Data
		{Path: containers + "/com.apple.Mail/Data/Library/Caches", Bytes: 2 << 30},
		{Path: containers + "/COM.APPLE.Notes/Data/Library/Caches", Bytes: 2 << 30},
		{Path: containers + "/com.small.app/Data/Library/Caches", Bytes: 10 << 20}, // under 50 MB
		{Path: containers + "/com.file.app/Data/Library/Caches", Kind: model.NodeFile, Bytes: 200 << 20},
		{Path: w2Home + "/Library/Caches/com.vendor.app", Bytes: 200 << 20}, // another folder
	}
	fs, err := w2Run(t, "app.container_caches", recipes.Env{Nodes: nodes})
	w2WantTargets(t, fs, err, containers+"/com.vendor.app/Data/Library/Caches", containers+"/Other.App/data/library/caches")
	checkTargets(t, w2Home, "app.container_caches", fs)
}

// The lint exemption is for this one recipe shape and nothing wider.
func TestContainerCachesExemptionIsNarrow(t *testing.T) {
	c, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := c.Get("app.container_caches")
	for name, mut := range map[string]func(*recipes.Recipe){
		"SAFE tier":      func(r *recipes.Recipe) { r.Risk = model.RiskSafe },
		"RISKY tier":     func(r *recipes.Recipe) { r.Risk = model.RiskRisky },
		"other detector": func(r *recipes.Recipe) { r.Detector = "children" },
		"wider pattern":  func(r *recipes.Recipe) { r.Allow = []string{"~/Library/Containers/*/Data/Library/*"} },
		"whole data":     func(r *recipes.Recipe) { r.Allow = []string{"~/Library/Containers/*/Data"} },
		"two patterns":   func(r *recipes.Recipe) { r.Allow = append(r.Allow, "~/Library/Containers/*/Data/Documents") },
	} {
		r := base
		r.Allow = append([]string(nil), base.Allow...)
		mut(&r)
		if recipes.Validate(r) == nil {
			t.Errorf("%s: the exemption must not apply", name)
		}
	}
}

// An Apple container's caches are refused at plan and apply time even though
// the allow pattern's wildcard could match them.
func TestAppleContainerCachesAreRefused(t *testing.T) {
	pol, err := safety.NewPolicy(w2Home, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := recipes.Load()
	r, _ := c.Get("app.container_caches")
	for _, target := range []string{
		containers + "/com.apple.Mail/Data/Library/Caches",
		containers + "/COM.APPLE.Notes/Data/Library/Caches",
	} {
		if err := pol.CheckTarget(target, r.Allow); err == nil {
			t.Errorf("%s was accepted", target)
		}
	}
}
