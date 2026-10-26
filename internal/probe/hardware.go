package probe

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"macdoc/internal/platform"
)

// Commands used by the hardware collector. Fixed argv, no shell.
var (
	cmdSysctlHW = platform.Command{Name: "sysctl", Args: []string{"hw.model", "hw.memsize", "hw.ncpu", "machdep.cpu.brand_string"}}
	cmdSwVers   = platform.Command{Name: "sw_vers"}
)

// runReadOnly runs cmd through r and returns stdout. A run error or a
// non-zero exit is an error labeled with the command and, for non-zero
// exits, the trimmed stderr.
func runReadOnly(ctx context.Context, r platform.Runner, cmd platform.Command) ([]byte, error) {
	label := platform.Key(cmd)
	res, err := r.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("probe: %s: %w", label, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			return nil, fmt.Errorf("probe: %s: exit status %d", label, res.ExitCode)
		}
		return nil, fmt.Errorf("probe: %s: exit status %d: %s", label, res.ExitCode, msg)
	}
	return res.Stdout, nil
}

// ParseSysctl parses `sysctl key1 key2 ...` output ("key: value" lines).
func ParseSysctl(out []byte) (map[string]string, error) {
	m := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("probe: sysctl: malformed line %q", line)
		}
		m[key] = strings.TrimSpace(val)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("probe: sysctl: %w", err)
	}
	if len(m) == 0 {
		return nil, errors.New("probe: sysctl: empty output")
	}
	return m, nil
}

// ParseSwVers parses `sw_vers` output into version and build.
func ParseSwVers(out []byte) (version, build string, err error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "ProductVersion":
			version = strings.TrimSpace(val)
		case "BuildVersion":
			build = strings.TrimSpace(val)
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", fmt.Errorf("probe: sw_vers: %w", err)
	}
	if version == "" {
		return "", "", errors.New("probe: sw_vers: no ProductVersion")
	}
	return version, build, nil
}

// CollectHardware runs sysctl + sw_vers through r.
func CollectHardware(ctx context.Context, r platform.Runner) (Hardware, error) {
	var hw Hardware
	out, err := runReadOnly(ctx, r, cmdSysctlHW)
	if err != nil {
		return hw, err
	}
	kv, err := ParseSysctl(out)
	if err != nil {
		return hw, err
	}
	hw.Model = kv["hw.model"]
	hw.Chip = kv["machdep.cpu.brand_string"]
	if hw.Model == "" {
		return hw, errors.New("probe: sysctl: missing hw.model")
	}
	ncpu, err := strconv.Atoi(kv["hw.ncpu"])
	if err != nil || ncpu <= 0 {
		return hw, fmt.Errorf("probe: sysctl: bad hw.ncpu %q", kv["hw.ncpu"])
	}
	hw.CPUCount = ncpu
	mem, err := parsePositiveInt(kv["hw.memsize"])
	if err != nil {
		return hw, fmt.Errorf("probe: sysctl: bad hw.memsize %q", kv["hw.memsize"])
	}
	hw.MemoryBytes = mem

	out, err = runReadOnly(ctx, r, cmdSwVers)
	if err != nil {
		return hw, err
	}
	hw.OSVersion, hw.OSBuild, err = ParseSwVers(out)
	return hw, err
}

// parsePositiveInt parses a decimal int64 greater than zero.
func parsePositiveInt(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("not positive: %d", n)
	}
	return n, nil
}
