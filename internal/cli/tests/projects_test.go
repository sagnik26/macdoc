package tests

import (
	"bytes"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
)

func sampleProjects() engine.ProjectsResult {
	d := tuiFixture().Projects
	d.Scan, d.ScanAge = model.ScanInfo{ID: "S-TEST"}, "2h ago"
	return d
}

func TestRenderProjects(t *testing.T) {
	var buf bytes.Buffer
	if err := cli.RenderProjects(&buf, cli.FilterProjects(sampleProjects(), 0), "/Users/me", 0); err != nil {
		t.Fatal(err)
	}
	want := `macdoc doctor projects · scan S-TEST · 2h ago

 Projects ────────────────────────────────────────────────── 802 MB in 2
  ~/code/app
    idle 120 days (git commit) · 800 MB · uncommitted changes
    node_modules 500 MB · ios/Pods 300 MB
  ~/Desktop/site
    idle time unknown · 2.0 MB · git clean
    .next 2.0 MB
    In a protected folder: listed only, macdoc never moves anything here.

 DerivedData of deleted projects ──────────────────────────────── 900 MB
     900 MB  ~/old/Gone.xcodeproj

 macdoc doctor wins · nothing has been changed
`
	if got := buf.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFilterProjects(t *testing.T) {
	res := sampleProjects()
	if got := cli.FilterProjects(res, 90).Projects; len(got) != 1 || got[0].Root != "/Users/me/code/app" {
		t.Errorf("--idle 90: %+v (unknown idle must not count as idle)", got)
	}
	if got := cli.FilterProjects(res, 200).Projects; len(got) != 0 || got == nil {
		t.Errorf("--idle 200: %+v", got)
	}
	res.Projects[1].ArtifactBytes = 999_999
	if got := cli.FilterProjects(res, 0).Projects; len(got) != 1 {
		t.Errorf("projects under 1 MB must be hidden: %+v", got)
	}
	var buf bytes.Buffer
	_ = cli.RenderProjects(&buf, cli.FilterProjects(res, 200), "/Users/me", 200)
	mustContain(t, buf.String(), "No projects idle 200 days or more with build artifacts.")
}
