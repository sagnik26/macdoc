package probe

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"macdoc/internal/platform"
)

// Commands used by the memory collector. Fixed argv, no shell.
var (
	cmdVMStat         = platform.Command{Name: "vm_stat"}
	cmdMemsize        = platform.Command{Name: "sysctl", Args: []string{"-n", "hw.memsize"}}
	cmdMemoryPressure = platform.Command{Name: "memory_pressure", Args: []string{"-Q"}}
	cmdPressureLevel  = platform.Command{Name: "sysctl", Args: []string{"-n", "kern.memorystatus_vm_pressure_level"}}
)

// vm_stat labels mapped to the Memory field each one fills.
var vmStatFields = map[string]func(m *Memory) *int64{
	"Pages free":                   func(m *Memory) *int64 { return &m.FreeBytes },
	"Pages active":                 func(m *Memory) *int64 { return &m.ActiveBytes },
	"Pages inactive":               func(m *Memory) *int64 { return &m.InactiveBytes },
	"Pages wired down":             func(m *Memory) *int64 { return &m.WiredBytes },
	"Pages occupied by compressor": func(m *Memory) *int64 { return &m.CompressedBytes },
}

// ParseVMStat parses `vm_stat` output (page size from its header line).
// TotalBytes, FreePercent and Pressure are left for the collector to fill.
func ParseVMStat(out []byte) (Memory, error) {
	m := Memory{FreePercent: -1, Pressure: PressureUnknown}
	var pageSize int64
	seen := make(map[string]bool, len(vmStatFields))
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if pageSize == 0 {
			if _, rest, ok := strings.Cut(line, "(page size of "); ok {
				num, _, ok := strings.Cut(rest, " bytes)")
				ps, err := parsePositiveInt(num)
				if !ok || err != nil {
					return Memory{}, fmt.Errorf("probe: vm_stat: bad page size in %q", line)
				}
				pageSize = ps
				continue
			}
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		field, known := vmStatFields[strings.TrimSpace(key)]
		if !known {
			continue
		}
		pages, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(val), "."), 10, 64)
		if err != nil || pages < 0 {
			return Memory{}, fmt.Errorf("probe: vm_stat: bad value in %q", line)
		}
		*field(&m) = pages // converted to bytes once the page size is known
		seen[strings.TrimSpace(key)] = true
	}
	if err := sc.Err(); err != nil {
		return Memory{}, fmt.Errorf("probe: vm_stat: %w", err)
	}
	if pageSize == 0 {
		return Memory{}, errors.New("probe: vm_stat: missing page size header")
	}
	for label, field := range vmStatFields {
		if !seen[label] {
			return Memory{}, fmt.Errorf("probe: vm_stat: missing %q", label)
		}
		p := field(&m)
		if *p > math.MaxInt64/pageSize {
			return Memory{}, fmt.Errorf("probe: vm_stat: %q overflows", label)
		}
		*p *= pageSize
	}
	return m, nil
}

// ParseMemoryPressure parses `memory_pressure -Q` output: free percentage.
func ParseMemoryPressure(out []byte) (freePercent int, err error) {
	const label = "System-wide memory free percentage:"
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		_, rest, ok := strings.Cut(sc.Text(), label)
		if !ok {
			continue
		}
		s := strings.TrimSuffix(strings.TrimSpace(rest), "%")
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 100 {
			return 0, fmt.Errorf("probe: memory_pressure: bad free percentage %q", rest)
		}
		return n, nil
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("probe: memory_pressure: %w", err)
	}
	return 0, errors.New("probe: memory_pressure: no free percentage line")
}

// ParsePressureLevel maps `sysctl -n kern.memorystatus_vm_pressure_level`
// output (1 normal, 2 warn, 4 critical) to a Pressure; anything else is
// PressureUnknown.
func ParsePressureLevel(out []byte) Pressure {
	switch strings.TrimSpace(string(out)) {
	case "1":
		return PressureNormal
	case "2":
		return PressureWarn
	case "4":
		return PressureCritical
	default:
		return PressureUnknown
	}
}

// CollectMemory runs vm_stat, sysctl hw.memsize, memory_pressure -Q and the
// pressure-level sysctl through r. Partial failures fill what they can.
// Only a vm_stat failure is returned as an error; otherwise TotalBytes stays
// 0, FreePercent -1 and Pressure PressureUnknown when their command fails.
func CollectMemory(ctx context.Context, r platform.Runner) (Memory, error) {
	out, err := runReadOnly(ctx, r, cmdVMStat)
	if err != nil {
		return Memory{FreePercent: -1, Pressure: PressureUnknown}, err
	}
	m, err := ParseVMStat(out)
	if err != nil {
		return Memory{FreePercent: -1, Pressure: PressureUnknown}, err
	}

	if out, err := runReadOnly(ctx, r, cmdMemsize); err == nil {
		if n, err := parsePositiveInt(string(out)); err == nil {
			m.TotalBytes = n
		}
	}
	if out, err := runReadOnly(ctx, r, cmdMemoryPressure); err == nil {
		if n, err := ParseMemoryPressure(out); err == nil {
			m.FreePercent = n
		}
	}
	if out, err := runReadOnly(ctx, r, cmdPressureLevel); err == nil {
		m.Pressure = ParsePressureLevel(out)
	}
	return m, nil
}
