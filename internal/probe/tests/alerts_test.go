package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/probe"
)

// Captured shape of `ps -axo pid=,ppid=,stat=,pcpu=,comm=` from macOS.
const psStateOut = `    1     0 Ss    0.0 /sbin/launchd
  200     1 Ss    0.3 /Applications/Foo.app/Contents/MacOS/Foo
  201   200 Z     0.0 (helper)
  202   200 Z+    0.0 (helper)
  300     1 R    97.5 /usr/bin/yes
  301     1 S    79.9 /usr/bin/almost
  302     1 Ss   12.0 /Applications/Cool App.app/Contents/MacOS/Cool App
  400  9999 Z     0.0 (orphan)
`

// Positive: zombie children are counted per parent, and a process at or over
// the limit is busy. Look-alikes: a zombie is never "busy", a process just
// under the limit is not reported, and a parent that is gone is flagged.
func TestParseProcessAlerts(t *testing.T) {
	a, err := probe.ParseProcessAlerts([]byte(psStateOut))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Zombies) != 2 || a.Zombies[0].PID != 200 || a.Zombies[0].Zombies != 2 || a.Zombies[0].Name != "Foo" || !a.Zombies[0].ParentOK {
		t.Errorf("zombies = %+v; want Foo with 2 first", a.Zombies)
	}
	if a.Zombies[1].PID != 9999 || a.Zombies[1].ParentOK {
		t.Errorf("an unlisted parent must be flagged: %+v", a.Zombies[1])
	}
	if len(a.Busy) != 1 || a.Busy[0].PID != 300 || a.Busy[0].CPUPercent != 97.5 {
		t.Errorf("busy = %+v; want only pid 300", a.Busy)
	}
}

// A listing that cannot be read is an error, never an all-clear.
func TestParseProcessAlertsRefusesGarbage(t *testing.T) {
	if _, err := probe.ParseProcessAlerts([]byte("not a process list\n")); err == nil {
		t.Error("garbage output should be an error")
	}
	a, err := probe.ParseProcessAlerts([]byte("1 0 Ss 0.0 /sbin/launchd\n"))
	if err != nil || len(a.Zombies) != 0 || a.Busy == nil || a.Zombies == nil {
		t.Errorf("a quiet system = %+v, %v; want empty lists, not null", a, err)
	}
}

// Each list is capped so a runaway system stays readable.
func TestProcessAlertsAreCapped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		b.WriteString("5" + string(rune('0'+i%10)) + "0 1 R 99.0 /usr/bin/spin\n")
	}
	a, err := probe.ParseProcessAlerts([]byte(b.String()))
	if err != nil || len(a.Busy) != 5 {
		t.Errorf("busy = %d, %v; want 5", len(a.Busy), err)
	}
}

func TestCollectProcessAlertsUsesTheFixedCommand(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"ps -axo pid=,ppid=,stat=,pcpu=,comm=": {Stdout: []byte(psStateOut)},
	}}
	a, err := probe.CollectProcessAlerts(context.Background(), fake)
	if err != nil || len(a.Zombies) != 2 {
		t.Errorf("got %+v, %v", a, err)
	}
}
