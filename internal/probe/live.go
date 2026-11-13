package probe

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/platform"
)

// This file reads what changes second to second: CPU and load, network
// traffic, disk traffic and the battery. Like the rest of the package it is
// read-only: every command goes through platform.Runner and the parsers are
// pure functions over captured output.

// CPU is a snapshot of processor use.
type CPU struct {
	UsagePercent  float64 `json:"usage_percent"` // 100 minus idle
	UserPercent   float64 `json:"user_percent"`
	SystemPercent float64 `json:"system_percent"`
	Load1         float64 `json:"load_1"`
	Load5         float64 `json:"load_5"`
	Load15        float64 `json:"load_15"`
	LogicalCores  int     `json:"logical_cores"`
}

var topCPULine = regexp.MustCompile(`CPU usage:\s*([\d.]+)% user,\s*([\d.]+)% sys,\s*([\d.]+)% idle`)

// ParseTopCPU reads the last "CPU usage" line of `top -l 2`. The first sample
// of top is averaged since boot, so the last one is the current use.
func ParseTopCPU(out []byte) (user, system, idle float64, err error) {
	var last []string
	for _, line := range strings.Split(string(out), "\n") {
		if m := topCPULine.FindStringSubmatch(line); m != nil {
			last = m
		}
	}
	if last == nil {
		return 0, 0, 0, errors.New("probe: no CPU usage line in top output")
	}
	user, _ = strconv.ParseFloat(last[1], 64)
	system, _ = strconv.ParseFloat(last[2], 64)
	idle, _ = strconv.ParseFloat(last[3], 64)
	return user, system, idle, nil
}

// ParseLoadAvg reads `sysctl -n vm.loadavg`: "{ 3.19 3.60 4.00 }".
func ParseLoadAvg(out []byte) (l1, l5, l15 float64, err error) {
	f := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}"))
	if len(f) < 3 {
		return 0, 0, 0, fmt.Errorf("probe: unexpected load average %q", strings.TrimSpace(string(out)))
	}
	vals := make([]float64, 3)
	for i := range vals {
		if vals[i], err = strconv.ParseFloat(f[i], 64); err != nil {
			return 0, 0, 0, fmt.Errorf("probe: load average %q: %w", f[i], err)
		}
	}
	return vals[0], vals[1], vals[2], nil
}

// CollectCPU runs top (two samples, a second apart) and sysctl.
func CollectCPU(ctx context.Context, r platform.Runner) (CPU, error) {
	var c CPU
	top, err := RunReadOnly(ctx, r, platform.Command{Name: "top", Args: []string{"-l", "2", "-n", "0", "-s", "1"}})
	if err != nil {
		return c, err
	}
	user, system, idle, err := ParseTopCPU(top)
	if err != nil {
		return c, err
	}
	c.UserPercent, c.SystemPercent, c.UsagePercent = user, system, 100-idle
	if out, err := RunReadOnly(ctx, r, platform.Command{Name: "sysctl", Args: []string{"-n", "vm.loadavg"}}); err == nil {
		c.Load1, c.Load5, c.Load15, _ = ParseLoadAvg(out)
	}
	if out, err := RunReadOnly(ctx, r, platform.Command{Name: "sysctl", Args: []string{"-n", "hw.logicalcpu"}}); err == nil {
		c.LogicalCores, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	return c, nil
}

// NetworkRates is the traffic on the physical network interfaces.
type NetworkRates struct {
	InBytesPerSec  float64 `json:"in_bytes_per_sec"`
	OutBytesPerSec float64 `json:"out_bytes_per_sec"`
}

var physicalInterface = regexp.MustCompile(`^en\d+$`)

// ParseNetstatIB sums the received and sent bytes of the physical interfaces
// (en0, en1, ...) from `netstat -ib`. Tunnels, loopback and virtual
// interfaces are left out, so the same traffic is not counted twice. Fields
// are read from the end of each row because the address column can be empty.
func ParseNetstatIB(out []byte) (in, sent int64, err error) {
	seen := false
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || !physicalInterface.MatchString(f[0]) || !strings.HasPrefix(f[2], "<Link#") {
			continue
		}
		ib, err1 := strconv.ParseInt(f[len(f)-5], 10, 64)
		ob, err2 := strconv.ParseInt(f[len(f)-2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		in, sent, seen = in+ib, sent+ob, true
	}
	if !seen {
		return 0, 0, errors.New("probe: no physical network interface in netstat output")
	}
	return in, sent, nil
}

// CollectNetwork reads the interface counters twice, interval apart, and
// returns the rate between them.
func CollectNetwork(ctx context.Context, r platform.Runner, interval time.Duration) (NetworkRates, error) {
	read := func() (int64, int64, error) {
		out, err := RunReadOnly(ctx, r, platform.Command{Name: "netstat", Args: []string{"-ib"}})
		if err != nil {
			return 0, 0, err
		}
		return ParseNetstatIB(out)
	}
	in1, out1, err := read()
	if err != nil {
		return NetworkRates{}, err
	}
	select {
	case <-ctx.Done():
		return NetworkRates{}, ctx.Err()
	case <-time.After(interval):
	}
	in2, out2, err := read()
	if err != nil {
		return NetworkRates{}, err
	}
	secs := interval.Seconds()
	return NetworkRates{
		InBytesPerSec:  float64(max(0, in2-in1)) / secs,
		OutBytesPerSec: float64(max(0, out2-out1)) / secs,
	}, nil
}

// DiskIO is the traffic to all disks over one second.
type DiskIO struct {
	MBPerSec        float64 `json:"mb_per_sec"`
	TransfersPerSec float64 `json:"transfers_per_sec"`
}

// ParseIostat reads the last row of `iostat -d -c 2 -w 1`. Each disk adds a
// group of three columns (KB/t, tps, MB/s); the rows are summed over disks.
func ParseIostat(out []byte) (DiskIO, error) {
	var last []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || len(f)%3 != 0 {
			continue
		}
		if _, err := strconv.ParseFloat(f[0], 64); err != nil {
			continue // a header row
		}
		last = f
	}
	if last == nil {
		return DiskIO{}, errors.New("probe: no data row in iostat output")
	}
	var io DiskIO
	for i := 0; i+2 < len(last); i += 3 {
		tps, err1 := strconv.ParseFloat(last[i+1], 64)
		mb, err2 := strconv.ParseFloat(last[i+2], 64)
		if err1 != nil || err2 != nil {
			return DiskIO{}, errors.New("probe: unexpected iostat row")
		}
		io.TransfersPerSec += tps
		io.MBPerSec += mb
	}
	return io, nil
}

// CollectDiskIO runs iostat for two one-second samples; the second is the
// current rate.
func CollectDiskIO(ctx context.Context, r platform.Runner) (DiskIO, error) {
	out, err := RunReadOnly(ctx, r, platform.Command{Name: "iostat", Args: []string{"-d", "-c", "2", "-w", "1"}})
	if err != nil {
		return DiskIO{}, err
	}
	return ParseIostat(out)
}

// Power is the battery and power source. On a Mac with no battery, Present
// is false and the other fields are empty.
type Power struct {
	Present       bool    `json:"present"`
	OnBattery     bool    `json:"on_battery"`
	Percent       int     `json:"percent"`
	State         string  `json:"state"`          // "charging", "discharging", "charged", ...
	Remaining     string  `json:"remaining"`      // "13:10", or "" when macOS has no estimate
	CycleCount    int     `json:"cycle_count"`    // -1 when unknown
	HealthPercent int     `json:"health_percent"` // -1 when unknown
	TemperatureC  float64 `json:"temperature_c"`  // 0 when unknown
}

var battLine = regexp.MustCompile(`(\d+)%;\s*([^;]+);\s*(?:(\d+:\d+) remaining|\(no estimate\)|[^;]*)`)

// ParsePmsetBatt reads `pmset -g batt`.
func ParsePmsetBatt(out []byte) Power {
	p := Power{CycleCount: -1, HealthPercent: -1}
	text := string(out)
	p.OnBattery = strings.Contains(text, "'Battery Power'")
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "InternalBattery") {
			continue
		}
		p.Present = true
		if m := battLine.FindStringSubmatch(line); m != nil {
			p.Percent, _ = strconv.Atoi(m[1])
			p.State = strings.TrimSpace(m[2])
			p.Remaining = m[3]
		}
	}
	return p
}

// ioregInt matches a top-level `"Key" = 123` line of `ioreg -rn
// AppleSmartBattery`. Only the keys asked for are read: the same dump holds
// the battery's serial number, which this package never reads.
func ioregInt(text, key string) (int, bool) {
	re := regexp.MustCompile(`(?m)^\s*"` + regexp.QuoteMeta(key) + `"\s*=\s*(-?\d+)\s*$`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// ApplyBatteryRegistry fills the cycle count, health and temperature from
// `ioreg -rn AppleSmartBattery`. On Apple silicon MaxCapacity is already a
// percentage; where it is not, health is left unknown rather than guessed.
func ApplyBatteryRegistry(p Power, out []byte) Power {
	text := string(out)
	if n, ok := ioregInt(text, "CycleCount"); ok {
		p.CycleCount = n
	}
	if n, ok := ioregInt(text, "MaxCapacity"); ok && n > 0 && n <= 100 {
		p.HealthPercent = n
	}
	if n, ok := ioregInt(text, "Temperature"); ok && n > 0 {
		p.TemperatureC = float64(n) / 100
	}
	return p
}

// CollectPower runs pmset and, when there is a battery, ioreg.
func CollectPower(ctx context.Context, r platform.Runner) (Power, error) {
	out, err := RunReadOnly(ctx, r, platform.Command{Name: "pmset", Args: []string{"-g", "batt"}})
	if err != nil {
		return Power{CycleCount: -1, HealthPercent: -1}, err
	}
	p := ParsePmsetBatt(out)
	if !p.Present {
		return p, nil
	}
	if reg, err := RunReadOnly(ctx, r, platform.Command{Name: "ioreg", Args: []string{"-rn", "AppleSmartBattery"}}); err == nil {
		p = ApplyBatteryRegistry(p, reg)
	}
	return p, nil
}
