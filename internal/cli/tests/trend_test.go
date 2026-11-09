package tests

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
)

func TestRenderTrend(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.Local)
	floor := int64(100 << 20)
	res := engine.TrendResult{
		From: model.ScanInfo{StartedAt: t0, TotalBytes: 100 * gb}, To: model.ScanInfo{StartedAt: t0.AddDate(0, 0, 30), TotalBytes: 112 * gb},
		Days: 30, SpanDays: 30, KeptFloor: floor, Total: engine.Range{Low: 12 * gb, High: 12 * gb},
		Areas: []engine.TrendArea{
			{Name: "Gradle", Before: engine.Range{Low: 4 * gb, High: 4 * gb}, After: engine.Range{Low: 10 * gb, High: 10 * gb}, Change: engine.Range{Low: 6 * gb, High: 6 * gb}},
			{Name: "AI models", Before: engine.Range{High: floor}, After: engine.Range{Low: 4 * gb, High: 4 * gb}, Change: engine.Range{Low: 4*gb - floor, High: 4 * gb}},
			{Name: "Python", Before: engine.Range{Low: gb, High: gb + floor}, After: engine.Range{Low: gb, High: gb + floor}, Change: engine.Range{Low: -floor, High: floor}},
			{Name: "Downloads", Before: engine.Range{Low: gb, High: gb}, After: engine.Range{Low: gb, High: gb}},
			{Name: "Xcode", Before: engine.Range{Low: 9 * gb, High: 9 * gb}, After: engine.Range{Low: 7 * gb, High: 7 * gb}, Change: engine.Range{Low: -2 * gb, High: -2 * gb}},
		},
		Growing: []engine.TrendFolder{{Path: "/Users/me/.gradle/caches", After: 9 * gb, Change: engine.Range{Low: 5 * gb, High: 5 * gb}}},
	}
	var buf bytes.Buffer
	if err := cli.RenderTrend(&buf, res, "/Users/me"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	mustContain(t, out,
		"macdoc doctor trend · 1 Sep 12:00 → 1 Oct 12:00",
		"Scanned data +12.0 GB (100 GB → 112 GB)",
		"Gradle                         4.0 GB    10.0 GB  +6.0 GB\n",
		"AI models                     <105 MB     4.0 GB  +3.9 GB or more\n",
		"Python                        ≥1.0 GB    ≥1.0 GB  under 105 MB\n",
		"Downloads                      1.0 GB     1.0 GB  no change\n",
		"Xcode                          9.0 GB     7.0 GB  −2.0 GB\n",
		"Grew the most", "30 days",
		"+5.0 GB            ~/.gradle/caches (now 9.0 GB)",
		"nothing has been changed")
	if strings.Contains(out, "covers less") {
		t.Error("note shown without one")
	}
}

func TestTrendCommand(t *testing.T) {
	home := cliFixture(t) // one scan
	if _, err := run(t, "trend"); err == nil || !strings.Contains(err.Error(), "only one scan so far") {
		t.Errorf("one scan: %v", err)
	}
	st, err := store.Open(store.DefaultPath(home))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	id, _ := st.BeginScan("/", "test", at)
	_ = st.AddNodes(id, []model.Node{{Path: home + "/.gradle", Kind: model.NodeDir, Bytes: 2 * gb}})
	_ = st.FinishScan(id, model.ScanComplete, 102*gb, 1, at)
	_ = st.Close()

	out, err := run(t, "trend")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Scanned data +2.0 GB", "covers less than the 30 days", "Gradle", "+1.9 GB or more", "~/.gradle")

	out, err = run(t, "trend", "--json", "--days", "7")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string             `json:"command"`
		Data    engine.TrendResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "doctor trend" || env.Data.Days != 7 || len(env.Data.Areas) != 1 {
		t.Errorf("envelope = %+v", env)
	}
	if _, err := run(t, "trend", "--days", "400"); err == nil {
		t.Error("--days 400 accepted")
	}
}
