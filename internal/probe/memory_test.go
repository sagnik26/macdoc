package probe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"macdoc/internal/platform"
)

func TestParseVMStatGolden(t *testing.T) {
	m, err := ParseVMStat(w1Fixture(t, "vm_stat.txt"))
	if err != nil {
		t.Fatal(err)
	}
	const ps = 16384
	want := Memory{
		FreeBytes:       3544 * ps,
		ActiveBytes:     117617 * ps,
		InactiveBytes:   113795 * ps,
		WiredBytes:      95215 * ps,
		CompressedBytes: 153219 * ps,
		FreePercent:     -1,
		Pressure:        PressureUnknown,
	}
	if m != want {
		t.Fatalf("got %+v, want %+v", m, want)
	}
}

func TestParseVMStat4K(t *testing.T) {
	m, err := ParseVMStat(w1Fixture(t, "w1_vm_stat_4k.txt"))
	if err != nil {
		t.Fatal(err)
	}
	const ps = 4096
	if m.FreeBytes != 10000*ps || m.ActiveBytes != 200000*ps || m.InactiveBytes != 150000*ps ||
		m.WiredBytes != 80000*ps || m.CompressedBytes != 100000*ps {
		t.Fatalf("got %+v", m)
	}
}

func TestParseVMStatBad(t *testing.T) {
	header := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\n"
	full := "Pages free: 1.\nPages active: 1.\nPages inactive: 1.\nPages wired down: 1.\nPages occupied by compressor: 1.\n"
	cases := map[string]string{
		"empty":          "",
		"garbage":        string(w1Fixture(t, "w1_garbage.txt")),
		"no header":      full,
		"bad page size":  "Mach Virtual Memory Statistics: (page size of lots bytes)\n" + full,
		"zero page size": "Mach Virtual Memory Statistics: (page size of 0 bytes)\n" + full,
		"missing field":  header + "Pages free: 1.\n",
		"bad value":      header + strings.Replace(full, "Pages active: 1.", "Pages active: x.", 1),
		"negative":       header + strings.Replace(full, "Pages active: 1.", "Pages active: -5.", 1),
		"overflow":       header + strings.Replace(full, "Pages free: 1.", "Pages free: 9223372036854775807.", 1),
	}
	for name, in := range cases {
		_, err := ParseVMStat([]byte(in))
		if err == nil {
			t.Errorf("%s: want error", name)
		} else if !strings.HasPrefix(err.Error(), "probe: vm_stat: ") {
			t.Errorf("%s: error %q lacks context", name, err)
		}
	}
}

func TestParseMemoryPressure(t *testing.T) {
	n, err := ParseMemoryPressure(w1Fixture(t, "memory_pressure_Q.txt"))
	if err != nil || n != 48 {
		t.Fatalf("got %d %v, want 48", n, err)
	}
	for name, in := range map[string]string{
		"empty":   "",
		"garbage": string(w1Fixture(t, "w1_garbage.txt")),
		"nan":     "System-wide memory free percentage: lots%\n",
		"over":    "System-wide memory free percentage: 101%\n",
	} {
		if _, err := ParseMemoryPressure([]byte(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestParsePressureLevel(t *testing.T) {
	if got := ParsePressureLevel(w1Fixture(t, "pressure_level.txt")); got != PressureNormal {
		t.Errorf("golden: got %q", got)
	}
	for _, c := range []struct {
		in   string
		want Pressure
	}{
		{"1\n", PressureNormal}, {"2", PressureWarn}, {" 4 \n", PressureCritical},
		{"0", PressureUnknown}, {"3", PressureUnknown}, {"", PressureUnknown}, {"garbage", PressureUnknown},
	} {
		if got := ParsePressureLevel([]byte(c.in)); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func w1MemoryRunner(t *testing.T) *platform.FakeRunner {
	return &platform.FakeRunner{Responses: map[string]platform.Result{
		"vm_stat":              w1OK(w1Fixture(t, "vm_stat.txt")),
		"sysctl -n hw.memsize": w1OK([]byte("8589934592\n")),
		"memory_pressure -Q":   w1OK(w1Fixture(t, "memory_pressure_Q.txt")),
		"sysctl -n kern.memorystatus_vm_pressure_level": w1OK(w1Fixture(t, "pressure_level.txt")),
	}}
}

var w1MemoryCalls = []platform.Command{
	{Name: "vm_stat"},
	{Name: "sysctl", Args: []string{"-n", "hw.memsize"}},
	{Name: "memory_pressure", Args: []string{"-Q"}},
	{Name: "sysctl", Args: []string{"-n", "kern.memorystatus_vm_pressure_level"}},
}

func TestCollectMemory(t *testing.T) {
	f := w1MemoryRunner(t)
	m, err := CollectMemory(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalBytes != 8589934592 || m.FreePercent != 48 || m.Pressure != PressureNormal ||
		m.FreeBytes != 3544*16384 || m.CompressedBytes != 153219*16384 {
		t.Fatalf("got %+v", m)
	}
	w1AssertCalls(t, f, w1MemoryCalls...)
}

func TestCollectMemoryPartialFailure(t *testing.T) {
	f := w1MemoryRunner(t)
	f.Responses["sysctl -n hw.memsize"] = platform.Result{ExitCode: 1, Stderr: []byte("denied")}
	delete(f.Responses, "memory_pressure -Q")
	f.Errors = map[string]error{"memory_pressure -Q": errors.New("not found")}
	f.Responses["sysctl -n kern.memorystatus_vm_pressure_level"] = w1OK([]byte("7\n"))

	m, err := CollectMemory(context.Background(), f)
	if err != nil {
		t.Fatalf("partial failure must not be an error: %v", err)
	}
	if m.TotalBytes != 0 || m.FreePercent != -1 || m.Pressure != PressureUnknown {
		t.Errorf("unknowns not marked: %+v", m)
	}
	if m.ActiveBytes != 117617*16384 {
		t.Errorf("vm_stat part lost: %+v", m)
	}
	w1AssertCalls(t, f, w1MemoryCalls...)
}

func TestCollectMemoryUnparsableExtras(t *testing.T) {
	f := w1MemoryRunner(t)
	f.Responses["sysctl -n hw.memsize"] = w1OK([]byte("garbage"))
	f.Responses["memory_pressure -Q"] = w1OK([]byte("garbage"))
	m, err := CollectMemory(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalBytes != 0 || m.FreePercent != -1 || m.Pressure != PressureNormal {
		t.Errorf("got %+v", m)
	}
}

func TestCollectMemoryVMStatError(t *testing.T) {
	f := w1MemoryRunner(t)
	f.Responses["vm_stat"] = platform.Result{ExitCode: 1, Stderr: []byte("vm_stat: failed\n")}
	m, err := CollectMemory(context.Background(), f)
	if err == nil || err.Error() != "probe: vm_stat: exit status 1: vm_stat: failed" {
		t.Fatalf("err = %v", err)
	}
	if m.FreePercent != -1 || m.Pressure != PressureUnknown {
		t.Errorf("unknowns not marked: %+v", m)
	}
	w1AssertCalls(t, f, cmdVMStat)
}

func TestCollectMemoryVMStatGarbage(t *testing.T) {
	f := w1MemoryRunner(t)
	f.Responses["vm_stat"] = w1OK(w1Fixture(t, "w1_garbage.txt"))
	if _, err := CollectMemory(context.Background(), f); err == nil {
		t.Fatal("want error")
	}
	w1AssertCalls(t, f, cmdVMStat)
}
