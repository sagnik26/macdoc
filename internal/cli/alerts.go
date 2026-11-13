package cli

import (
	"fmt"
	"strings"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/probe"
)

// alertLines describes zombie parents and busy processes, one line each. It
// returns nothing when there is nothing to report or ps could not be read.
func alertLines(a *probe.ProcessAlerts) []string {
	if a == nil {
		return nil
	}
	var out []string
	for _, z := range a.Zombies {
		kids := fmt.Sprintf("%d finished child %s", z.Zombies, plural(z.Zombies, "process", "processes"))
		if z.ParentOK {
			out = append(out, fmt.Sprintf("%s (pid %d) has %s it never collected. Quitting it clears them.", z.Name, z.PID, kids))
			continue
		}
		out = append(out, fmt.Sprintf("%s are waiting on pid %d, which is not in the process list. Restarting the Mac clears them.", kids, z.PID))
	}
	for _, p := range a.Busy {
		out = append(out, fmt.Sprintf("%s (pid %d) is using %.0f%% of a CPU core.", p.Name, p.PID, p.CPUPercent))
	}
	return out
}

// writeAlerts adds the "Process alerts" section when there is something to say.
func writeAlerts(b *strings.Builder, t *Theme, a *probe.ProcessAlerts) {
	lines := alertLines(a)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s\n", t.heading("Process alerts"))
	for _, l := range lines {
		fmt.Fprintf(b, "  %s %s\n", t.sev(engine.SeverityWarn), t.Wrap(t.text.Render(l), 4))
	}
}
