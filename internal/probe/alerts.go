package probe

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sagnik26/macdoc/internal/platform"
)

// CmdPSState lists pid, parent pid, state, CPU share and command of every
// process. Fixed argv, no shell; it never signals or changes any process.
var CmdPSState = platform.Command{Name: "ps", Args: []string{"-axo", "pid=,ppid=,stat=,pcpu=,comm="}}

// HotCPUPercent is the CPU share above which one process is reported as busy.
// ps reports a decaying recent average, so a process over this is busy over
// the last while, not for a single instant.
const HotCPUPercent = 80.0

// maxAlerts caps each list so a runaway system stays readable.
const maxAlerts = 5

// ZombieParent is a process whose finished children were never collected.
// macdoc only reports it: ending a zombie needs its parent to reap it or quit.
type ZombieParent struct {
	PID      int    `json:"pid"`
	Name     string `json:"name"`
	Zombies  int    `json:"zombies"`
	ParentOK bool   `json:"parent_known"` // false when the parent is not in the list (pid 1 or gone)
}

// BusyProcess is one process using a lot of CPU.
type BusyProcess struct {
	PID        int     `json:"pid"`
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpu_percent"`
}

// ProcessAlerts is what is wrong with the running processes right now.
type ProcessAlerts struct {
	Zombies []ZombieParent `json:"zombie_parents"`
	Busy    []BusyProcess  `json:"busy_processes"`
}

// ParseProcessAlerts parses `ps -axo pid=,ppid=,stat=,pcpu=,comm=`. Lines that
// do not start with four fields are skipped; output with no valid line is an
// error, so an unreadable listing is never reported as "all clear".
func ParseProcessAlerts(out []byte) (ProcessAlerts, error) {
	names := map[int]string{}
	zombies := map[int]int{} // parent pid -> zombie children
	var busy []BusyProcess
	parsed := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		cpu, e3 := strconv.ParseFloat(f[3], 64)
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		parsed++
		comm := strings.Join(f[4:], " ")
		names[pid] = GroupName(comm)
		if strings.HasPrefix(f[2], "Z") {
			zombies[ppid]++
			continue
		}
		if cpu >= HotCPUPercent {
			busy = append(busy, BusyProcess{PID: pid, Name: names[pid], CPUPercent: cpu})
		}
	}
	if err := sc.Err(); err != nil {
		return ProcessAlerts{}, fmt.Errorf("probe: ps: %w", err)
	}
	if parsed == 0 {
		return ProcessAlerts{}, errors.New("probe: ps: no process lines")
	}
	res := ProcessAlerts{Zombies: []ZombieParent{}, Busy: []BusyProcess{}}
	for ppid, n := range zombies {
		name, ok := names[ppid]
		if !ok {
			name = "unknown"
		}
		res.Zombies = append(res.Zombies, ZombieParent{PID: ppid, Name: name, Zombies: n, ParentOK: ok})
	}
	sort.Slice(res.Zombies, func(i, j int) bool {
		if res.Zombies[i].Zombies != res.Zombies[j].Zombies {
			return res.Zombies[i].Zombies > res.Zombies[j].Zombies
		}
		return res.Zombies[i].PID < res.Zombies[j].PID
	})
	sort.Slice(busy, func(i, j int) bool { return busy[i].CPUPercent > busy[j].CPUPercent })
	res.Busy = append(res.Busy, busy...)
	if len(res.Zombies) > maxAlerts {
		res.Zombies = res.Zombies[:maxAlerts]
	}
	if len(res.Busy) > maxAlerts {
		res.Busy = res.Busy[:maxAlerts]
	}
	return res, nil
}

// CollectProcessAlerts runs ps through r and reports zombie parents and busy
// processes.
func CollectProcessAlerts(ctx context.Context, r platform.Runner) (ProcessAlerts, error) {
	out, err := RunReadOnly(ctx, r, CmdPSState)
	if err != nil {
		return ProcessAlerts{}, err
	}
	return ParseProcessAlerts(out)
}
