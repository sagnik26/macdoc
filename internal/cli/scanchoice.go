package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sagnik26/macdoc/internal/engine"
)

// lastScan describes the scan the dashboard could reuse: how old it is and
// how much it measured. ok is false when there is none, so there is nothing to
// choose between.
func (m TuiModel) lastScan() (age string, bytes int64, ok bool) {
	switch {
	case m.Data != nil:
		return m.Data.Res.ScanAge, m.Data.Res.Scan.TotalBytes, true
	case m.Intro && m.IntroStatus.HasScan:
		return m.IntroStatus.Age, m.IntroStatus.Bytes, true
	}
	return "", 0, false
}

// askScan is /scan. With an earlier scan to reuse it asks whether to scan
// again or keep the last data; with none it just scans.
func (m TuiModel) askScan() (tea.Model, tea.Cmd) {
	if m.Loading {
		return m, nil
	}
	if _, _, ok := m.lastScan(); !ok {
		m.Intro = false
		return m, m.StartLoad(true)
	}
	m.Overlay, m.scroll, m.scanPick = overlayScan, 0, 0
	return m, nil
}

// scanKey handles the scan choice. ↑↓ move the selector, enter runs the
// highlighted choice (the last scan is preselected), and u/f still work as
// shortcuts. Other keys (esc closes it) fall through to the overlay's usual
// keys.
func (m TuiModel) scanKey(k string) (tea.Model, tea.Cmd, bool) {
	switch k {
	case "up", "k", "down", "j", "tab", "shift+tab":
		m.scanPick = 1 - m.scanPick
		return m, nil, true
	case "f", "s":
		return m.rescan()
	case "u", "l":
		next, cmd := m.useLastScan()
		return next, cmd, true
	case "enter", " ":
		if m.scanPick == 1 {
			return m.rescan()
		}
		next, cmd := m.useLastScan()
		return next, cmd, true
	case "esc", "q", "?":
		return m, nil, false
	}
	return m, nil, true
}

// rescan closes the choice, leaves the welcome screen and starts a fresh scan.
func (m TuiModel) rescan() (tea.Model, tea.Cmd, bool) {
	m.Overlay, m.scroll, m.Intro = OverlayNone, 0, false
	cmd := m.StartLoad(true)
	return m, cmd, true
}

// useLastScan keeps the data macdoc already has.
func (m TuiModel) useLastScan() (tea.Model, tea.Cmd) {
	age, _, _ := m.lastScan()
	m.Overlay, m.scroll = OverlayNone, 0
	if m.Intro {
		return m, m.leaveIntro(false)
	}
	m.Flash = fmt.Sprintf("Using the last scan (%s). Press r for a fresh one.", age)
	return m, nil
}

// scanBody is the choice shown by /scan: just the two options with a
// selector, the last scan preselected.
func (m TuiModel) scanBody() string {
	t := m.t
	age, bytes, _ := m.lastScan()
	taken := "taken " + age
	if bytes > 0 {
		taken += ", " + engine.DecimalBytes(bytes) + " measured"
	}
	opts := []struct{ name, tag, desc string }{
		{"Use the last scan", "recommended", taken + ". Opens instantly."},
		{"Scan again", "", "Reads your disk again (about 1–2 min); Press \"esc\" to stop."},
	}
	var b strings.Builder
	for i, o := range opts {
		mark, name := "  ", t.accent.Render(o.name)
		if i == m.scanPick {
			mark, name = t.brand.Render("▶ "), t.Title.Render(o.name)
		}
		tag := ""
		if o.tag != "" {
			tag = "  " + t.Ok.Render(o.tag)
		}
		b.WriteString(" " + mark + name + tag + "\n")
		b.WriteString("     " + t.muted.Render(o.desc) + "\n\n")
	}
	return b.String()
}
