package tests

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
)

// Benchmarks for the node index at the size of the S4 experiment
// (docs/experiments/S4-node-prefix-query): 1,000 projects of 300 folders.
// They do not run with the ordinary tests. Run:
//
//	go test ./internal/engine/tests -run '^$' -bench 'Scan(300k|5k)' -benchtime 3x

// offRunner refuses every command, so a detector can never run a real tool.
type offRunner struct{}

func (offRunner) Run(context.Context, platform.Command) (platform.Result, error) {
	return platform.Result{}, errors.New("commands are off in this benchmark")
}

// countNodes counts the questions a detector pass asks.
type countNodes struct {
	src      recipes.NodeSource
	n        atomic.Int64
	rows     atomic.Int64
	slowest  atomic.Int64 // nanoseconds
	slowName atomic.Value
}

func (c *countNodes) Nodes(prefix string, maxDepth int) ([]model.Node, error) {
	c.n.Add(1)
	t0 := time.Now()
	ns, err := c.src.Nodes(prefix, maxDepth)
	d := time.Since(t0).Nanoseconds()
	c.rows.Add(int64(len(ns)))
	if d > c.slowest.Load() {
		c.slowest.Store(d)
		c.slowName.Store(fmt.Sprintf("Nodes(%q, %d) = %d rows", prefix, maxDepth, len(ns)))
	}
	return ns, err
}

func speedNodes(speedHome string, projects, dirs int) []model.Node {
	var ns []model.Node
	add := func(p string, b int64) {
		ns = append(ns, model.Node{Path: p, Kind: model.NodeDir, Bytes: b, Files: 10, MtimeMax: 1, Depth: strings.Count(p, "/")})
	}
	for p := range projects {
		proj := fmt.Sprintf("%s/code/p%04d", speedHome, p)
		add(proj, int64(p+1)*1_000_000)
		for d := range dirs - 1 {
			add(fmt.Sprintf("%s/d%03d", proj, d), int64((p*7+d*13)%9973)*1000)
		}
	}
	// A few folders the detectors look at, so they find something to size.
	for _, d := range []string{"Library/Caches", "Library/Developer/Xcode/DerivedData", ".npm", ".cache", "Library/Logs"} {
		add(speedHome+"/"+d, 5_000_000_000)
		for i := range 20 {
			add(fmt.Sprintf("%s/%s/item%02d", speedHome, d, i), int64(i+1)*20_000_000)
		}
	}
	return ns
}

func heapNow() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// BenchmarkScan300k is the S4 size: 300,000 nodes.
func BenchmarkScan300k(b *testing.B) { benchScan(b, 1000, 300) }

// BenchmarkScan5k is closer to a real scan, whose kept set is small.
func BenchmarkScan5k(b *testing.B) { benchScan(b, 20, 250) }

// speedScan is one stored scan the benchmarks share.
type speedScan struct {
	home     string
	st       *store.Store
	id       string
	nodes    int
	prefixes []string // one folder per project
}

func buildSpeedScan(b *testing.B, projects, dirs int) speedScan {
	b.Helper()
	home, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	at := time.Now().Add(-time.Hour)
	id, err := st.BeginScan("/", "bench", at)
	if err != nil {
		b.Fatal(err)
	}
	ns := speedNodes(home, projects, dirs)
	for i := 0; i < len(ns); i += 10_000 {
		if err := st.AddNodes(id, ns[i:min(i+10_000, len(ns))]); err != nil {
			b.Fatal(err)
		}
	}
	if err := st.FinishScan(id, model.ScanComplete, 0, 0, at.Add(time.Minute)); err != nil {
		b.Fatal(err)
	}
	b.Logf("%d nodes", len(ns))
	s := speedScan{home: home, st: st, id: id, nodes: len(ns), prefixes: make([]string, projects)}
	for i := range s.prefixes {
		s.prefixes[i] = fmt.Sprintf("%s/code/p%04d", home, i)
	}
	return s
}

// source returns the store-backed or the indexed way to ask the scan.
func (s speedScan) source(name string) engine.ScanNodes {
	if name == "index" {
		return engine.NewScanNodes(s.st, s.id)
	}
	return engine.ScanNodes{St: s.st, ScanID: s.id}
}

var speedKinds = []string{"store", "index"}

func benchScan(b *testing.B, projects, dirs int) {
	s := buildSpeedScan(b, projects, dirs)
	// One question about one folder's children, the index already loaded.
	b.Run("Call", func(b *testing.B) {
		for _, name := range speedKinds {
			b.Run(name, func(b *testing.B) { s.benchCall(b, s.source(name)) })
		}
	})
	b.Run("Load", s.benchLoad)
	// Every recipe's detector, one after the other, against one scan.
	b.Run("Detectors", func(b *testing.B) {
		for _, name := range speedKinds {
			b.Run(name, func(b *testing.B) { s.benchDetectors(b, name) })
		}
	})
	// The whole of `macdoc reclaim`: open the store, run the detectors with the
	// worker pool, classify and rank.
	b.Run("Opportunities", s.benchOpportunities)
}

func (s speedScan) benchCall(b *testing.B, sn engine.ScanNodes) {
	_, _ = sn.Nodes(s.prefixes[0], 1) // loads the index, if there is one
	b.ResetTimer()
	for i := range b.N {
		if _, err := sn.Nodes(s.prefixes[i%len(s.prefixes)], 1); err != nil {
			b.Fatal(err)
		}
	}
}

// heapPeak samples the heap until stop is closed and sends the highest value.
func heapPeak(stop chan struct{}, done chan uint64) {
	var top uint64
	var m runtime.MemStats
	for {
		select {
		case <-stop:
			done <- top
			return
		case <-time.After(time.Millisecond):
			runtime.ReadMemStats(&m)
			top = max(top, m.HeapAlloc)
		}
	}
}

// benchLoad times the first question, which reads the whole scan, and
// reports what the index keeps afterwards and its peak while loading.
func (s speedScan) benchLoad(b *testing.B) {
	var keep engine.ScanNodes
	var before, after, peak uint64
	for range b.N {
		before = heapNow()
		stop, done := make(chan struct{}), make(chan uint64)
		go heapPeak(stop, done)
		keep = engine.NewScanNodes(s.st, s.id)
		if _, err := keep.Nodes(s.prefixes[0], 1); err != nil {
			b.Fatal(err)
		}
		close(stop)
		peak = <-done
		after = heapNow()
	}
	runtime.KeepAlive(keep)
	b.ReportMetric(float64(after-before)/1e6, "kept-MB")
	b.ReportMetric(float64(peak-before)/1e6, "peak-MB")
	b.ReportMetric(float64(after-before)/float64(s.nodes), "bytes/node")
}

func (s speedScan) benchDetectors(b *testing.B, kind string) {
	cat, err := recipes.Load()
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	var c *countNodes
	for range b.N {
		c = &countNodes{src: s.source(kind)} // a new source per pass: each scan pays its own load
		env := recipes.Env{Home: s.home, Nodes: c, Runner: offRunner{}, Now: time.Now()}
		for _, r := range cat.Recipes {
			if d, ok := recipes.Lookup(r.Detector); ok {
				_, _ = d(ctx, env, r)
			}
		}
	}
	b.ReportMetric(float64(c.n.Load()), "nodes-calls/pass")
	b.ReportMetric(float64(c.rows.Load()), "rows/pass")
	b.ReportMetric(float64(c.slowest.Load())/1e6, "slowest-call-ms")
	if name, ok := c.slowName.Load().(string); ok {
		b.Logf("slowest call: %s", name)
	}
}

func (s speedScan) benchOpportunities(b *testing.B) {
	for range b.N {
		engine.ProjectCache.Lock()
		engine.ProjectCache.Key = ""
		engine.ProjectCache.Unlock()
		_, err := engine.Opportunities(context.Background(), engine.OpportunityOptions{
			Home: s.home, StorePath: filepath.Join(s.home, "state.db"), Runner: offRunner{}, Now: time.Now(),
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
