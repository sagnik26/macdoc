package tests

import (
	"errors"
	"github.com/sagnik26/macdoc/internal/engine"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
)

type trendScan struct {
	at    time.Time
	total int64
	nodes map[string]int64 // path → bytes; "~" is the home folder
}

// trendStore writes the scans into a new store and returns its path. Node
// paths starting with "~" are under trendHome.
func trendStore(t *testing.T, scans ...trendScan) string {
	t.Helper()
	path := store.DefaultPath(t.TempDir())
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for _, s := range scans {
		id, err := st.BeginScan("/", "test", s.at)
		if err != nil {
			t.Fatal(err)
		}
		var nodes []model.Node
		for p, b := range s.nodes {
			nodes = append(nodes, model.Node{Path: strings.Replace(p, "~", trendHome, 1), Kind: model.NodeDir, Bytes: b})
		}
		if err := st.AddNodes(id, nodes); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishScan(id, model.ScanComplete, s.total, 1, s.at); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

const trendHome = "/Users/me"

func area(res engine.TrendResult, name string) (engine.TrendArea, bool) {
	for _, a := range res.Areas {
		if a.Name == name {
			return a, true
		}
	}
	return engine.TrendArea{}, false
}

func TestTrend(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	path := trendStore(t,
		trendScan{t0, 100 * gb, map[string]int64{"~/.gradle": 4 * gb, "~/Library/Developer/Xcode": 30 * gb, "~/Downloads": 2 * gb}},
		trendScan{t0.AddDate(0, 0, 3), 105 * gb, map[string]int64{"~/.gradle": 5 * gb}},
		trendScan{t0.AddDate(0, 0, 35), 112 * gb, map[string]int64{
			"~/.gradle": 10 * gb, "~/.gradle/caches": 9 * gb, "~/.gradle/caches/modules-2": 8_500_000_000,
			"~/Library/Developer/Xcode": 26 * gb, "~/.ollama": 4 * gb, "~/.ollama/models": 4 * gb}},
	)
	res, err := engine.Trend(engine.TrendOptions{StorePath: path, Home: trendHome, Days: 30})
	if err != nil {
		t.Fatal(err)
	}
	// 30 days back from day 35 is day 5: the newest scan at or before it is day 3.
	if !res.From.StartedAt.Equal(t0.AddDate(0, 0, 3)) || res.SpanDays != 32 || res.Note != "" {
		t.Errorf("from %v span %d note %q", res.From.StartedAt, res.SpanDays, res.Note)
	}
	if res.Total != (engine.Range{Low: 7 * gb, High: 7 * gb}) {
		t.Errorf("total = %+v", res.Total)
	}
	g, ok := area(res, "Gradle")
	if !ok || g.Change != (engine.Range{Low: 5 * gb, High: 5 * gb}) || !g.Change.Exact() {
		t.Errorf("gradle = %+v", g)
	}
	// Xcode wasn't stored in the day-3 scan: under the floor then.
	x, _ := area(res, "Xcode")
	if x.Before != (engine.Range{Low: 0, High: engine.KeptFloor}) || x.Change != (engine.Range{Low: 26*gb - engine.KeptFloor, High: 26 * gb}) {
		t.Errorf("xcode = %+v", x)
	}
	if _, ok := area(res, "Trash"); ok {
		t.Error("an area never stored is listed")
	}
	if res.Areas[0].Name != "Xcode" {
		t.Errorf("areas not sorted by growth: %s first", res.Areas[0].Name)
	}

	// Growing: the deepest folder that explains most of a growth.
	var got []string
	for _, f := range res.Growing {
		got = append(got, strings.TrimPrefix(f.Path, trendHome))
	}
	if strings.Join(got, " ") != "/Library/Developer/Xcode /.gradle/caches/modules-2 /.ollama/models" {
		t.Errorf("growing = %v", got)
	}

	// A window longer than the history falls back to the oldest scan.
	res, err = engine.Trend(engine.TrendOptions{StorePath: path, Home: trendHome, Days: 90})
	if err != nil {
		t.Fatal(err)
	}
	if !res.From.StartedAt.Equal(t0) || !strings.Contains(res.Note, "35 days of scans so far") {
		t.Errorf("fallback from %v note %q", res.From.StartedAt, res.Note)
	}
	if d, _ := area(res, "Downloads"); d.Change != (engine.Range{Low: -2 * gb, High: engine.KeptFloor - 2*gb}) {
		t.Errorf("downloads = %+v", d)
	}
}

func TestTrendGrowingFilters(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	path := trendStore(t,
		trendScan{t0, gb, map[string]int64{
			"/Applications/App.app": 2 * gb, "/Applications/App.app/V1": 2 * gb,
			"~/Library": 10 * gb, "~/Library/A": 2 * gb, "~/Library/B": 2 * gb}},
		trendScan{t0.AddDate(0, 0, 7), gb, map[string]int64{
			// An update replaced V1 with V2: the app didn't grow.
			"/Applications/App.app": 2 * gb, "/Applications/App.app/V2": 2 * gb,
			// ~/Library grew only by what A and B grew.
			"~/Library": 12 * gb, "~/Library/A": 3 * gb, "~/Library/B": 3 * gb}},
	)
	res, err := engine.Trend(engine.TrendOptions{StorePath: path, Home: trendHome})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range res.Growing {
		got = append(got, strings.TrimPrefix(f.Path, trendHome))
	}
	if strings.Join(got, " ") != "/Library/A /Library/B" {
		t.Errorf("growing = %v", got)
	}
	if engine.SpanText(7*24*time.Hour) != "7 days" || engine.SpanText(18*time.Hour) != "18 hours" || engine.SpanText(25*time.Hour) != "1 day" {
		t.Error("SpanText")
	}
}

func TestTrendNeedsTwoScans(t *testing.T) {
	path := trendStore(t, trendScan{time.Now(), gb, nil})
	if _, err := engine.Trend(engine.TrendOptions{StorePath: path, Home: trendHome}); !errors.Is(err, engine.ErrNoHistory) {
		t.Errorf("one scan: %v", err)
	}
	empty := store.DefaultPath(t.TempDir())
	if _, err := engine.Trend(engine.TrendOptions{StorePath: empty, Home: trendHome}); !errors.Is(err, store.ErrNoScan) {
		t.Errorf("no scan: %v", err)
	}
}
