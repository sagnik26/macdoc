package tests

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
)

// These tests drive the dashboard with messages and a fake range walk to show
// how the detail pane starts, cancels, caches and shows it (ADR 0005, stage 3).

const (
	idFirst  = "O-ab12cd" // a measured quarantine folder with a target
	idSecond = "O-k3m9pq" // another one, so the selection can move between two
	idNative = "O-z4n1ty" // not a quarantine folder: no range can apply
)

// walker is the fake walk. It counts calls, notes cancellations and the most
// walks it saw at once, and can hold each walk until released.
type walker struct {
	calls, canceled atomic.Int32
	gate            chan struct{} // when set, a walk waits for it or for cancel
	span            *engine.Span
	err             error

	mu      sync.Mutex
	now     int
	most    int
	targets []string
}

func (w *walker) measure(ctx context.Context, _ *cli.TuiDeps, op model.Opportunity) (*engine.Span, error) {
	w.calls.Add(1)
	w.mu.Lock()
	w.now++
	w.most = max(w.most, w.now)
	w.targets = append(w.targets, op.Target)
	w.mu.Unlock()
	defer func() { w.mu.Lock(); w.now--; w.mu.Unlock() }()
	if w.gate != nil {
		select {
		case <-w.gate:
		case <-ctx.Done():
			w.canceled.Add(1)
			return nil, ctx.Err()
		}
	}
	return w.span, w.err
}

func rangeData() *cli.TuiData {
	d := tuiFixture()
	ops := d.Res.Opportunities.Opportunities
	ops[0].Target = "/Users/me/Library/Developer/Xcode/DerivedData/MyApp"
	// The second suggestion becomes a folder of the same kind.
	ops[1].Method, ops[1].Command, ops[1].Bytes = model.MethodQuarantine, nil, measured(11_000_000_000)
	ops[1].Target = "/Users/me/Library/Caches/Other"
	return d
}

func lowHigh() *engine.Span {
	hi := int64(1_900_000_000)
	return &engine.Span{Lower: 1_200_000_000, Upper: &hi}
}

// rangeTUI is a loaded dashboard on the Suggestions tab. Nothing runs until
// the caller runs the commands it gets back from send.
func rangeTUI(t *testing.T, w, h int, wk *walker) cli.TuiModel {
	t.Helper()
	deps := &cli.TuiDeps{Home: "/Users/me", Measure: wk.measure,
		Load: func(context.Context, *cli.TuiDeps, bool) (*cli.TuiData, error) { return rangeData(), nil }}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h}, cli.LoadedMsg{Data: rangeData()})
	return m
}

// send delivers one message and returns the new model with the commands that
// walk (the ones that will produce a SpanMsg), not yet started.
func send(t *testing.T, m cli.TuiModel, msg tea.Msg) (cli.TuiModel, []tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	if cmd == nil {
		return next.(cli.TuiModel), nil
	}
	return next.(cli.TuiModel), []tea.Cmd{cmd}
}

// results runs every command in cmds off the test goroutine and returns a
// channel of the SpanMsgs they produce. A batch is run member by member.
func results(cmds []tea.Cmd) <-chan cli.SpanMsg {
	out := make(chan cli.SpanMsg, 8)
	var wg sync.WaitGroup
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		defer wg.Done()
		switch msg := c().(type) {
		case cli.SpanMsg:
			out <- msg
		case tea.BatchMsg:
			for _, sub := range msg {
				if sub != nil {
					wg.Add(1)
					go run(sub)
				}
			}
		}
	}
	for _, c := range cmds {
		wg.Add(1)
		go run(c)
	}
	go func() { wg.Wait(); close(out) }()
	return out
}

// next waits for one walk result and fails the test, instead of hanging, when
// none arrives.
func next(t *testing.T, ch <-chan cli.SpanMsg, what string) cli.SpanMsg {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatalf("%s: no result was produced", what)
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: no result within 5 s", what)
	}
	return cli.SpanMsg{}
}

// release lets one waiting walk finish, and fails the test when none is waiting.
func release(t *testing.T, wk *walker) {
	t.Helper()
	select {
	case wk.gate <- struct{}{}:
	case <-time.After(5 * time.Second):
		t.Fatal("no walk was waiting to be released")
	}
}

// only returns the one SpanMsg a set of commands produces, or fails.
func only(t *testing.T, cmds []tea.Cmd) cli.SpanMsg {
	t.Helper()
	select {
	case msg, ok := <-results(cmds):
		if !ok {
			t.Fatal("no command produced a walk result")
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("the walk did not finish")
	}
	return cli.SpanMsg{}
}

// noWalk fails if the commands produce a walk result.
func noWalk(t *testing.T, wk *walker, before int32, cmds []tea.Cmd) {
	t.Helper()
	for range results(cmds) {
		t.Fatal("a walk command was returned")
	}
	if got := wk.calls.Load(); got != before {
		t.Errorf("walks = %d, want %d", got, before)
	}
}

// eventually waits for cond, which a goroutine makes true.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDashboardStartsOneWalkAndShowsTheRange(t *testing.T) {
	wk := &walker{span: lowHigh()}
	m := rangeTUI(t, 100, 40, wk)
	m, walks := send(t, m, keys("2")[0]) // the tab switch alone shows no detail on a narrow screen
	noWalk(t, wk, 0, walks)
	m, walks = send(t, m, keys("e")[0])
	if out := m.Screen(); !strings.Contains(out, "measuring the range…") {
		t.Errorf("no progress line while the walk runs:\n%s", out)
	}
	msg := only(t, walks)
	if wk.calls.Load() != 1 || msg.ID != idFirst {
		t.Fatalf("walks = %d for %q, want one for %s", wk.calls.Load(), msg.ID, idFirst)
	}
	if got := wk.targets[0]; got != "/Users/me/Library/Developer/Xcode/DerivedData/MyApp" {
		t.Errorf("walked %q, not the suggestion's own target", got)
	}
	m, walks = send(t, m, msg)
	noWalk(t, wk, 1, walks)
	out := flat(m.Screen())
	if strings.Contains(out, "measuring the range") {
		t.Errorf("progress line left after the result:\n%s", out)
	}
	if !strings.Contains(out, "Removing this frees at least 1.2 GB and at most 1.9 GB. The difference is space shared with copies of its files.") {
		t.Errorf("no range sentence:\n%s", out)
	}
	if strings.Contains(out, flat(cli.SizeNote)) {
		t.Errorf("the plain note is shown beside the range:\n%s", out)
	}
}

func TestDashboardWideStartsWhenTheTabOpens(t *testing.T) {
	wk := &walker{span: lowHigh()}
	m := rangeTUI(t, 140, 40, wk)
	m, walks := send(t, m, keys("2")[0]) // the detail is already beside the list
	if !strings.Contains(m.Screen(), "measuring the range…") {
		t.Errorf("no progress line on the wide layout:\n%s", m.Screen())
	}
	if only(t, walks).ID != idFirst || wk.calls.Load() != 1 {
		t.Errorf("want one walk for %s, got %d calls", idFirst, wk.calls.Load())
	}
}

func TestDashboardStartsNoWalkWhenNoRangeCanApply(t *testing.T) {
	wk := &walker{span: lowHigh()}
	m := rangeTUI(t, 140, 40, wk)
	// The overview tab shows no suggestion detail.
	if _, walks := send(t, m, tea.WindowSizeMsg{Width: 140, Height: 40}); len(walks) != 0 {
		t.Error("a command was returned on the overview tab")
	}
	m, walks := send(t, m, keys("2")[0])
	only(t, walks)
	m, walks = send(t, m, keys("down")[0])
	only(t, walks) // the second suggestion is a folder too
	// The third is a native command, so no walk is made and no line shows.
	m, walks = send(t, m, keys("down")[0])
	noWalk(t, wk, 2, walks)
	if out := m.Screen(); strings.Contains(out, "measuring the range") || !strings.Contains(out, idNative) {
		t.Errorf("want the native item's pane without a progress line:\n%s", out)
	}
	// A folder with an estimated size, or with no target, is not walked either.
	d := rangeData()
	d.Res.Opportunities.Opportunities[0].Bytes.Source = model.SourceEstimated
	d.Res.Opportunities.Opportunities[1].Target = ""
	wk2 := &walker{span: lowHigh()}
	m2 := rangeTUI(t, 140, 40, wk2)
	m2 = step(t, m2, cli.LoadedMsg{Data: d})
	m2, walks = send(t, m2, keys("2")[0])
	noWalk(t, wk2, 0, walks)
	_, walks = send(t, m2, keys("down")[0])
	noWalk(t, wk2, 0, walks)
}

func TestDashboardIgnoresAStaleResultAndCancelsOnMove(t *testing.T) {
	wk := &walker{gate: make(chan struct{}), span: lowHigh()}
	m := rangeTUI(t, 140, 40, wk)
	m, first := send(t, m, keys("2")[0])
	firstOut := results(first)
	eventually(t, "the first walk to start", func() bool { return wk.calls.Load() == 1 })

	m, second := send(t, m, keys("down")[0]) // the selection moves to another suggestion
	eventually(t, "the first walk to be canceled", func() bool { return wk.canceled.Load() == 1 })
	secondOut := results(second)
	stale := next(t, firstOut, "the canceled walk") // it reports back
	if stale.ID != idFirst || !errors.Is(stale.Err, context.Canceled) {
		t.Fatalf("first walk result = %+v, want canceled for %s", stale, idFirst)
	}

	// Even a successful result for the first suggestion must not be used.
	stale.Err, stale.Span = nil, lowHigh()
	eventually(t, "the second walk to start", func() bool { return wk.calls.Load() == 2 })
	m, none := send(t, m, stale)
	noWalk(t, wk, 2, none)
	if out := flat(m.Screen()); strings.Contains(out, "at most 1.9 GB") || !strings.Contains(out, "measuring the range") {
		t.Errorf("a stale result changed the pane:\n%s", out)
	}

	release(t, wk)
	live := next(t, secondOut, "the second walk")
	if live.ID != idSecond {
		t.Fatalf("second walk is for %s, want %s", live.ID, idSecond)
	}
	m, _ = send(t, m, live)
	if out := flat(m.Screen()); strings.Contains(out, "measuring the range") {
		t.Errorf("progress line left after the live result:\n%s", out)
	}
	wk.mu.Lock()
	most := wk.most
	wk.mu.Unlock()
	if most != 1 {
		t.Errorf("%d walks ran at once, want 1", most)
	}

	// The stale result was not cached: going back walks the first one again.
	_, back := send(t, m, keys("up")[0])
	go func() {
		select {
		case wk.gate <- struct{}{}:
		case <-time.After(5 * time.Second):
		}
	}()
	if only(t, back).ID != idFirst || wk.calls.Load() != 3 {
		t.Errorf("walks = %d, want a third for %s", wk.calls.Load(), idFirst)
	}
}

func TestDashboardCancelsOnTabChangeRescanAndLeaving(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"tab change", keys("tab")[0]},
		{"rescan", keys("r")[0]},
		{"help overlay", keys("?")[0]},
	}
	for _, c := range cases {
		wk := &walker{gate: make(chan struct{}), span: lowHigh()}
		m := rangeTUI(t, 140, 40, wk)
		m, walks := send(t, m, keys("2")[0])
		out := results(walks)
		eventually(t, c.name+": the walk to start", func() bool { return wk.calls.Load() == 1 })
		_, _ = send(t, m, c.msg)
		eventually(t, c.name+": the walk to be canceled", func() bool { return wk.canceled.Load() == 1 })
		if msg := next(t, out, c.name); !errors.Is(msg.Err, context.Canceled) {
			t.Errorf("%s: result = %+v, want canceled", c.name, msg)
		}
	}

	// A narrow screen leaves the detail with Esc.
	wk := &walker{gate: make(chan struct{})}
	m := rangeTUI(t, 100, 40, wk)
	m, _ = send(t, m, keys("2")[0])
	m, walks := send(t, m, keys("e")[0])
	results(walks)
	eventually(t, "the walk to start", func() bool { return wk.calls.Load() == 1 })
	send(t, m, keys("esc")[0])
	eventually(t, "esc to cancel the walk", func() bool { return wk.canceled.Load() == 1 })
}

func TestDashboardReusesAResultWithoutWalkingAgain(t *testing.T) {
	wk := &walker{span: lowHigh()}
	m := rangeTUI(t, 100, 40, wk)
	m, _ = send(t, m, keys("2")[0])
	m, walks := send(t, m, keys("e")[0])
	m, _ = send(t, m, only(t, walks))
	// Out, to the next suggestion and its walk, then back to the first.
	m, _ = send(t, m, keys("esc")[0])
	m, _ = send(t, m, keys("down")[0])
	m, walks = send(t, m, keys("e")[0])
	m, _ = send(t, m, only(t, walks))
	if wk.calls.Load() != 2 {
		t.Fatalf("walks = %d, want 2", wk.calls.Load())
	}
	m, _ = send(t, m, keys("esc")[0])
	m, _ = send(t, m, keys("up")[0])
	m, walks = send(t, m, keys("e")[0])
	noWalk(t, wk, 2, walks)
	if out := flat(m.Screen()); !strings.Contains(out, "at least 1.2 GB and at most 1.9 GB") {
		t.Errorf("the kept result is not shown:\n%s", out)
	}

	// A new scan forgets the results: IDs belong to one scan.
	m, _ = send(t, m, cli.LoadedMsg{Data: rangeData()})
	m, _ = send(t, m, keys("2")[0])
	_, walks = send(t, m, keys("e")[0])
	only(t, walks)
	if wk.calls.Load() != 3 {
		t.Errorf("walks = %d after a rescan, want 3", wk.calls.Load())
	}
}

func TestDashboardShowsNothingExtraWhenTheWalkFails(t *testing.T) {
	for name, wk := range map[string]*walker{
		"error":   {err: errors.New("walk failed: permission denied")},
		"skipped": {}, // Measure returns nothing: no sharing, or the folder is gone
	} {
		m := rangeTUI(t, 100, 40, wk)
		m, _ = send(t, m, keys("2")[0])
		m, walks := send(t, m, keys("e")[0])
		m, none := send(t, m, only(t, walks))
		noWalk(t, wk, 1, none)
		out := flat(m.Screen())
		if strings.Contains(out, "measuring the range") || strings.Contains(out, "at least") ||
			strings.Contains(out, "permission denied") || !strings.Contains(out, flat(cli.SizeNote)) {
			t.Errorf("%s: want the plain pane with the usual note:\n%s", name, out)
		}
		// Nothing is tried again for the same suggestion.
		_, again := send(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
		noWalk(t, wk, 1, again)
	}
}

func TestDashboardWithoutAWalkFunctionNeverWalks(t *testing.T) {
	deps := &cli.TuiDeps{Home: "/Users/me"}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40}, cli.LoadedMsg{Data: rangeData()})
	_, cmd := m.Update(keys("2")[0])
	if cmd != nil {
		t.Error("a command was returned with no walk function")
	}
}

func TestDashboardRangeOnRealClones(t *testing.T) {
	id := realClones(t, true)
	home, err := cli.UserHome()
	if err != nil {
		t.Fatal(err)
	}
	cat, err := cli.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	res, err := engine.Opportunities(context.Background(), engine.OpportunityOptions{Home: home, MinBytes: -1, Catalog: cat})
	if err != nil || len(res.Opportunities) != 1 || res.Opportunities[0].ID != id {
		t.Fatalf("opportunities: %v, %v", res.Opportunities, err)
	}
	var walked atomic.Int32
	deps := &cli.TuiDeps{Home: home, Measure: func(ctx context.Context, d *cli.TuiDeps, op model.Opportunity) (*engine.Span, error) {
		walked.Add(1)
		return engine.Measure(ctx, engine.OpportunityOptions{Home: d.Home}, op)
	}}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	data := &cli.TuiData{Res: engine.OverviewResult{Scan: res.Scan, Opportunities: res}}
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 40}, cli.LoadedMsg{Data: data})
	m, _ = send(t, m, keys("2")[0])
	m, walks := send(t, m, keys("e")[0])
	msg := only(t, walks)
	if msg.Err != nil || msg.Span == nil || msg.Span.Upper == nil {
		t.Fatalf("walk result = %+v, want a range", msg)
	}
	m, _ = send(t, m, msg)
	out := flat(m.Screen())
	if !strings.Contains(out, "Removing this frees at least ") || !strings.Contains(out, " and at most ") || walked.Load() != 1 {
		t.Errorf("no range on real clones (walks %d):\n%s", walked.Load(), out)
	}
}
