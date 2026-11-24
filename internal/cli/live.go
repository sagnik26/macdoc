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

// whyBox shows where memory pressure sits on its three levels, the two other
// facts macdoc weighs (swap in use and free disk) against their limits, and the
// rule. It shows with the red alert, and on the w key.
func (m TuiModel) whyBox(res engine.StatusResult) string {
	t := m.t
	const lead = "   "
	levels := []struct {
		name  string
		level probe.Pressure
		style func(string) string
	}{
		{"Normal", probe.PressureNormal, func(s string) string { return t.Ok.Bold(true).Render(s) }},
		{"Warn", probe.PressureWarn, func(s string) string { return t.warn.Bold(true).Render(s) }},
		{"Critical", probe.PressureCritical, func(s string) string { return t.bad.Bold(true).Render(s) }},
	}
	label := "Memory pressure   "
	ladder, marker := lead+label, lead+strings.Repeat(" ", lipgloss.Width(label))
	known := false
	for i, l := range levels {
		cell := t.faint.Render(l.name)
		if l.level == res.Report.Memory.Pressure {
			known = true
			cell = l.style(l.name)
			if !t.on {
				cell = "[" + l.name + "]"
			}
		}
		if i > 0 {
			ladder += t.faint.Render(" ─── ")
			marker += strings.Repeat(" ", 5)
		}
		if l.level == res.Report.Memory.Pressure {
			marker += t.accent.Render("▲ now")
			marker += strings.Repeat(" ", max(0, lipgloss.Width(l.name)-5))
		} else {
			marker += strings.Repeat(" ", lipgloss.Width(l.name))
		}
		ladder += cell
	}
	if !known {
		marker = lead + strings.Repeat(" ", lipgloss.Width(label)) + t.muted.Render("pressure couldn't be read")
	}
	rows := []string{ladder, strings.TrimRight(marker, " ")}
	for _, c := range engine.SlowChecks(res.Report) {
		mark, word := t.faint.Render("○"), t.muted.Render("fine")
		if c.Tripped {
			mark, word = t.warn.Render("●"), t.warn.Render(c.Flag)
		}
		rows = append(rows, fmt.Sprintf("%s%-17s %s %-10s %s  %s", lead, c.Name, mark, t.text.Render(c.Value), word, t.muted.Render("("+c.Limit+")")))
	}
	now := "Right now: no alert."
	if lvl, _ := engine.SlowVerdict(res.Report); lvl == engine.SeverityCritical || lvl == engine.SeverityWarn {
		now = "Right now: the alert is on."
	}
	rows = append(rows, "", lead+t.muted.Render("The red alert shows when pressure is Warn or Critical, or when the disk is low and swap is high."),
		lead+t.text.Render(now))
	return indentLines(strings.Join(rows, "\n")+"\n", " ") + "\n" // no title line: just the pressure ladder and the checks
}
