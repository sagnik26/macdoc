package cli

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sagnik26/macdoc/internal/probe"

	"github.com/sagnik26/macdoc/internal/engine"
)

// The live parts of the vitals page: how fresh the reading is, and which way
// each number moved since the last reading. The last few readings are kept in
// memory only while the page is open.

// histLen is how many readings are remembered.
const histLen = 30

// vitalsHist is the recent readings of each number on the page. A number the
// Mac couldn't read in a given reading is left out of that series.
type vitalsHist struct {
	score, cpu, mem, disk, down, up, power []float64
}

// push returns s with v added at the end, keeping the last histLen values.
// It copies, so older copies of the model never see the change.
func push(s []float64, v float64) []float64 {
	out := make([]float64, 0, min(len(s)+1, histLen))
	if len(s) >= histLen {
		s = s[len(s)-histLen+1:]
	}
	out = append(out, s...)
	return append(out, v)
}

// add records one reading.
func (h vitalsHist) add(res engine.StatusResult) vitalsHist {
	h.score = push(h.score, float64(res.Score))
	if c := res.CPU; c != nil {
		h.cpu = push(h.cpu, c.UsagePercent)
	}
	if m := res.Report.Memory; m.TotalBytes > 0 && m.FreePercent >= 0 {
		h.mem = push(h.mem, float64(100-m.FreePercent))
	}
	if d := res.Report.Disk; d.ContainerTotalBytes > 0 {
		h.disk = push(h.disk, float64(d.ContainerTotalBytes-d.ContainerFreeBytes)*100/float64(d.ContainerTotalBytes))
	}
	if n := res.Network; n != nil {
		h.down = push(h.down, n.InBytesPerSec)
		h.up = push(h.up, n.OutBytesPerSec)
	}
	if p := res.Power; p != nil && p.Present {
		h.power = push(h.power, float64(p.Percent))
	}
	return h
}

// liveTail is what follows a number on a live row: an arrow for the way it
// moved since the previous reading (eps is the smallest change that counts).
// upBad colors a rise as a warning and a fall as good.
func liveTail(t *Theme, s []float64, eps float64, upBad bool) string {
	arrow := t.faint.Render("▬")
	if n := len(s); n >= 2 {
		switch d := s[n-1] - s[n-2]; {
		case d > eps:
			arrow = t.muted.Render("▲")
			if upBad {
				arrow = t.warn.Render("▲")
			}
		case d < -eps:
			arrow = t.muted.Render("▼")
			if upBad {
				arrow = t.Ok.Render("▼")
			}
		}
	}
	return " " + arrow
}

// statusClockMsg ticks once a second while the vitals page is open, so "updated
// 3s ago" keeps counting between readings.
type statusClockMsg struct{}

func statusClock() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return statusClockMsg{} })
}

// statusBarMsg steps the first-reading bar's animation; gen says which opening of
// the page it belongs to, so a stale chain stops.
type statusBarMsg struct{ gen int }

func statusBarTick(gen int) tea.Cmd {
	return tea.Tick(33*time.Millisecond, func(time.Time) tea.Msg { return statusBarMsg{gen} })
}

// statusTarget is how far the first-reading bar should be: nothing but a sliver
// before the first report, then each finished probe plus half the next (so it keeps
// moving while the slowest one runs), and full once all have answered.
func (m TuiModel) statusTarget() float64 {
	switch {
	case m.statusTotal <= 0:
		return 0.03
	case m.statusDone >= m.statusTotal:
		return 1
	}
	return (float64(m.statusDone) + 0.5) / float64(m.statusTotal)
}

// barTick eases the drawn bar toward its target and keeps ticking until the first
// reading arrives or the page closes.
func (m TuiModel) barTick(msg statusBarMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.statusGen || m.Overlay != overlayVitals || m.status != nil {
		return m, nil
	}
	if target := m.statusTarget(); m.statusShown < target {
		m.statusShown = min(target, m.statusShown+max((target-m.statusShown)*0.15, 0.004))
	}
	return m, statusBarTick(m.statusGen)
}

// clockTick handles one tick: keep ticking while the page is open, and stop
// (letting a later open start a new chain) once it is closed.
func (m TuiModel) clockTick() (tea.Model, tea.Cmd) {
	if m.Overlay != overlayVitals {
		m.clockOn = false
		return m, nil
	}
	return m, statusClock()
}

// liveHeader is the line above the readings: whether they are current.
func (m TuiModel) liveHeader() string {
	t := m.t
	age := max(0, int(NowFunc().Sub(m.statusAt).Seconds()))
	dot := t.faint.Render("○")
	if age < 2 {
		dot = t.Ok.Bold(true).Render("●") // a fresh reading: the dot lights up
	}
	when := "just now"
	if age >= 1 {
		when = fmt.Sprintf("%ds ago", age)
	}
	return " " + dot + " " + t.text.Render("Live") + t.muted.Render(" · updated "+when+" · every "+fmt.Sprint(int(statusEvery.Seconds()))+"s") + "\n"
}

// gaugeStop is one stop of a gauge: its name and how it is drawn when it is the
// current one.
type gaugeStop struct {
	name  string
	style func(string) string
}

// gauge draws a three-stop gauge as two lines: the stops joined by rules, with the
// current one lit, and a "▲" marker under it. extra follows the stops (a value
// and its limit); unknown replaces the marker when cur is negative.
func (m TuiModel) gauge(label string, stops []gaugeStop, cur int, extra, unknown string) []string {
	t := m.t
	const lead = "   "
	label = fmt.Sprintf("%-18s", label)
	ladder, marker := lead+label, lead+strings.Repeat(" ", lipgloss.Width(label))
	for i, s := range stops {
		cell := t.faint.Render(s.name)
		if i == cur {
			cell = s.style(s.name)
			if !t.on {
				cell = "[" + s.name + "]"
			}
		}
		if i > 0 {
			ladder += t.faint.Render(" ─── ")
			marker += strings.Repeat(" ", 5)
		}
		if i == cur {
			w := lipgloss.Width(cell)
			pad := (w - 1) / 2 // under the middle of the word
			marker += strings.Repeat(" ", pad) + t.accent.Render("▲") + strings.Repeat(" ", max(0, w-1-pad))
		} else {
			marker += strings.Repeat(" ", lipgloss.Width(cell))
		}
		ladder += cell
	}
	if cur < 0 {
		marker = lead + strings.Repeat(" ", lipgloss.Width(label)) + t.muted.Render(unknown)
	}
	if extra != "" {
		ladder += "   " + t.muted.Render(extra)
	}
	return []string{ladder, strings.TrimRight(marker, " ")}
}

// whyBox shows where memory pressure, swap in use and free disk sit, each as a
// gauge with a "▲" marker: pressure on Normal, Warn and Critical, and the two
// other facts macdoc weighs against their limits. It shows with the red alert, and
// on the w key.
func (m TuiModel) whyBox(res engine.StatusResult) string {
	t := m.t
	ok := func(s string) string { return t.Ok.Bold(true).Render(s) }
	warn := func(s string) string { return t.warn.Bold(true).Render(s) }
	bad := func(s string) string { return t.bad.Bold(true).Render(s) }

	cur := -1
	switch res.Report.Memory.Pressure {
	case probe.PressureNormal:
		cur = 0
	case probe.PressureWarn:
		cur = 1
	case probe.PressureCritical:
		cur = 2
	}
	rows := m.gauge("Memory pressure", []gaugeStop{{"Normal", ok}, {"Warn", warn}, {"Critical", bad}}, cur, "", "pressure couldn't be read")

	stops := map[string][]gaugeStop{
		"Swap in use": {{"Low", ok}, {"Rising", warn}, {"High", bad}},
		"Free disk":   {{"Plenty", ok}, {"Getting low", warn}, {"Low", bad}},
	}
	for _, c := range engine.SlowChecks(res.Report) {
		rows = append(rows, m.gauge(c.Name, stops[c.Name], c.Zone, c.Value+" ("+c.Limit+")", "couldn't be read")...)
	}
	return indentLines(strings.Join(rows, "\n")+"\n", " ") + "\n" // no title line: just the gauges
}
