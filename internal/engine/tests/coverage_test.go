package tests

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// findEntry returns the software row with the given ID.
func findEntry(t *testing.T, res engine.SoftwareResult, id string) engine.SoftwareEntry {
	t.Helper()
	for _, sw := range res.Software {
		if sw.ID == id {
			return sw
		}
	}
	t.Fatalf("%s not listed: %+v", id, res.Software)
	return engine.SoftwareEntry{}
}

// Positive: a bundle named by a recipe's match is covered by that recipe, and
// a package named by match is covered too.
func TestSoftwareCoveredByMatchingRecipes(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"npm ls -g --depth=0 --json": {Stdout: []byte(`{"dependencies":{"yarn":{"version":"1.22.22"}}}`)},
	}}
	appBundle(t, apps, "Docker", "com.docker.docker", fake)
	f := newFixture(t, time.Hour)

	opts := softwareOpts(f, apps, fake)
	opts.HasTool = func(name string) bool { return name == "npm" }
	res, err := engine.Software(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	docker := findEntry(t, res, "app:com.docker.docker")
	if !slices.Contains(docker.CoveredBy, "docker.volumes") {
		t.Errorf("Docker should be covered by the docker recipes, got %v", docker.CoveredBy)
	}
	yarn := findEntry(t, res, "npm:yarn")
	if !slices.Contains(yarn.CoveredBy, "yarn.cache") {
		t.Errorf("yarn should be covered by yarn.cache, got %v", yarn.CoveredBy)
	}
}

// Look-alike: a different app whose name merely resembles a covered one is
// not covered, and neither is software no recipe names. Such software is
// advisory only (ADR 0007).
func TestSoftwareLookAlikeIsNotCovered(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	appBundle(t, apps, "Dockerish", "com.example.dockerish", fake)
	appBundle(t, apps, "Figma", "com.figma.Desktop", fake)
	f := newFixture(t, time.Hour)

	res, err := engine.Software(context.Background(), softwareOpts(f, apps, fake))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"app:com.example.dockerish", "app:com.figma.Desktop"} {
		sw := findEntry(t, res, id)
		if len(sw.CoveredBy) != 0 {
			t.Errorf("%s should be advisory only, covered by %v", id, sw.CoveredBy)
		}
		if sw.CoveredBy == nil {
			t.Errorf("%s: covered_by should be [] not null", id)
		}
	}
}

// The overview summary reads the stored snapshot: count, owned bytes, and
// the names new since the scan before. Without a snapshot it is nil.
func TestSoftwareSnapshotSummary(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	appBundle(t, apps, "Figma", "com.figma.Desktop", fake)

	home := evaluatedTempDir(t)
	cache := filepath.Join(home, "Library", "Caches", "com.figma.Desktop")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	first := newFixtureIn(t, home, 2*time.Hour, dirNode(cache, 300))
	if sum, err := engine.SoftwareSnapshot(first.storePath, first.scanID); err != nil || sum != nil {
		t.Fatalf("no snapshot yet: sum=%+v err=%v; want nil", sum, err)
	}
	if _, err := engine.Software(context.Background(), softwareOpts(first, apps, fake)); err != nil {
		t.Fatal(err)
	}

	appBundle(t, apps, "Slack", "com.tinyspeck.slackmacgap", fake)
	second := newFixtureIn(t, home, time.Hour)
	if _, err := engine.Software(context.Background(), softwareOpts(second, apps, fake)); err != nil {
		t.Fatal(err)
	}
	sum, err := engine.SoftwareSnapshot(second.storePath, second.scanID)
	if err != nil || sum == nil {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
	if sum.Count != 2 || sum.OwnedBytes != 0 || len(sum.NewNames) != 1 || sum.NewNames[0] != "Slack" {
		t.Errorf("summary = %+v; want 2 pieces of software, the Slack name as new", sum)
	}
}

// Positive and look-alike for tools on PATH: pip and uv are covered by the
// recipes that name them; pipx resembles pip but no recipe names it, so it is
// advisory only.
func TestSoftwareToolCoverage(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"pip", "uv", "pipx"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	f := newFixture(t, time.Hour)

	opts := softwareOpts(f, apps, fake)
	opts.BinDirs = []string{bin}
	res, err := engine.Software(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if sw := findEntry(t, res, "tool:pip"); !slices.Contains(sw.CoveredBy, "pip.cache") {
		t.Errorf("pip should be covered by pip.cache, got %v", sw.CoveredBy)
	}
	if sw := findEntry(t, res, "tool:uv"); !slices.Contains(sw.CoveredBy, "uv.cache") {
		t.Errorf("uv should be covered by uv.cache, got %v", sw.CoveredBy)
	}
	if sw := findEntry(t, res, "tool:pipx"); len(sw.CoveredBy) != 0 {
		t.Errorf("pipx is a look-alike of pip and must not be covered, got %v", sw.CoveredBy)
	}
}

// softwareIDFor writes a match entry's identity the way discovery reports the
// same software: a bundle is "app:<id>" and an app name is "app-name:<name>".
func softwareIDFor(kind, id string) string {
	switch kind {
	case "bundle":
		return "app:" + id
	case "app":
		return "app-name:" + id
	}
	return kind + ":" + id
}

// Every recipe that names software gets three checks, one subtest each for
// its match entries. Positive: the software it names is covered by it.
// Look-alike: a similar name, which is a different program, is not. Precondition:
// the recipe ends up with a known toolchain.
func TestEveryMatchedRecipeIsCoveredAndHasAToolchain(t *testing.T) {
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, r := range cat.Recipes {
		if len(r.Match) == 0 {
			continue
		}
		checked++
		t.Run(r.ID, func(t *testing.T) {
			if !slices.Contains(recipes.Toolchains, r.Toolchain) {
				t.Errorf("toolchain %q is not a known toolchain", r.Toolchain)
			}
			for _, m := range r.Match {
				kind, id, err := recipes.ParseMatch(m)
				if err != nil {
					t.Fatalf("match %q: %v", m, err)
				}
				if !slices.Contains(engine.CoveredBy(cat, softwareIDFor(kind, id)), r.ID) {
					t.Errorf("%s should cover %s", r.ID, m)
				}
				if slices.Contains(engine.CoveredBy(cat, softwareIDFor(kind, id+"2")), r.ID) {
					t.Errorf("%s covers %s2, a different program", r.ID, id)
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("no recipe has a match")
	}
}
