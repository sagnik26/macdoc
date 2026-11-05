package tests

import (
	"context"
	"errors"
	"macdoc/internal/probe"
	"strings"
	"testing"

	"macdoc/internal/platform"
)

func TestParseSysctlGolden(t *testing.T) {
	kv, err := probe.ParseSysctl(w1Fixture(t, "sysctl_hw.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"hw.model":                 "MacBookAir10,1",
		"hw.memsize":               "8589934592",
		"hw.ncpu":                  "8",
		"machdep.cpu.brand_string": "Apple M1",
	}
	for k, v := range want {
		if kv[k] != v {
			t.Errorf("%s = %q, want %q", k, kv[k], v)
		}
	}
	if len(kv) != len(want) {
		t.Errorf("got %d keys, want %d", len(kv), len(want))
	}
}

func TestParseSysctlIntel(t *testing.T) {
	kv, err := probe.ParseSysctl(w1Fixture(t, "w1_sysctl_hw_intel.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if kv["hw.model"] != "MacBookPro16,1" {
		t.Errorf("hw.model = %q", kv["hw.model"])
	}
	// The brand string itself contains no ": " but has "@" and parentheses.
	if got := kv["machdep.cpu.brand_string"]; got != "Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz" {
		t.Errorf("brand = %q", got)
	}
}

func TestParseSysctlBad(t *testing.T) {
	for name, in := range map[string][]byte{
		"empty":   nil,
		"blank":   []byte("\n\n"),
		"garbage": w1Fixture(t, "w1_garbage.txt"),
		"nokey":   []byte(": value\n"),
	} {
		if _, err := probe.ParseSysctl(in); err == nil {
			t.Errorf("%s: want error", name)
		} else if !strings.HasPrefix(err.Error(), "probe: sysctl: ") {
			t.Errorf("%s: error %q lacks context", name, err)
		}
	}
}

func TestParseSwVers(t *testing.T) {
	v, b, err := probe.ParseSwVers(w1Fixture(t, "sw_vers.txt"))
	if err != nil || v != "26.6.2" || b != "25G83" {
		t.Fatalf("got %q %q %v", v, b, err)
	}
	v, b, err = probe.ParseSwVers(w1Fixture(t, "w1_sw_vers_intel.txt"))
	if err != nil || v != "13.6.9" || b != "22G830" {
		t.Fatalf("intel: got %q %q %v", v, b, err)
	}
	for name, in := range map[string][]byte{"empty": nil, "garbage": w1Fixture(t, "w1_garbage.txt")} {
		if _, _, err := probe.ParseSwVers(in); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestCollectHardware(t *testing.T) {
	f := &platform.FakeRunner{Responses: map[string]platform.Result{
		platform.Key(probe.CmdSysctlHW): w1OK(w1Fixture(t, "sysctl_hw.txt")),
		platform.Key(probe.CmdSwVers):   w1OK(w1Fixture(t, "sw_vers.txt")),
	}}
	hw, err := probe.CollectHardware(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	want := probe.Hardware{Model: "MacBookAir10,1", Chip: "Apple M1", CPUCount: 8, MemoryBytes: 8589934592, OSVersion: "26.6.2", OSBuild: "25G83"}
	if hw != want {
		t.Fatalf("got %+v, want %+v", hw, want)
	}
	w1AssertCalls(t, f,
		platform.Command{Name: "sysctl", Args: []string{"hw.model", "hw.memsize", "hw.ncpu", "machdep.cpu.brand_string"}},
		platform.Command{Name: "sw_vers"},
	)
}

func TestCollectHardwareSysctlExitError(t *testing.T) {
	f := &platform.FakeRunner{Responses: map[string]platform.Result{
		platform.Key(probe.CmdSysctlHW): {ExitCode: 1, Stderr: []byte("  sysctl: unknown oid 'hw.model'\n")},
	}}
	_, err := probe.CollectHardware(context.Background(), f)
	if err == nil || err.Error() != "probe: sysctl hw.model hw.memsize hw.ncpu machdep.cpu.brand_string: exit status 1: sysctl: unknown oid 'hw.model'" {
		t.Fatalf("err = %v", err)
	}
	w1AssertCalls(t, f, probe.CmdSysctlHW)
}

func TestCollectHardwareSwVersRunError(t *testing.T) {
	boom := errors.New("boom")
	f := &platform.FakeRunner{
		Responses: map[string]platform.Result{platform.Key(probe.CmdSysctlHW): w1OK(w1Fixture(t, "sysctl_hw.txt"))},
		Errors:    map[string]error{platform.Key(probe.CmdSwVers): boom},
	}
	hw, err := probe.CollectHardware(context.Background(), f)
	if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "probe: sw_vers: ") {
		t.Fatalf("err = %v", err)
	}
	if hw.Model != "MacBookAir10,1" {
		t.Errorf("sysctl part should still be filled: %+v", hw)
	}
}

func TestCollectHardwareBadValues(t *testing.T) {
	cases := map[string]string{
		"no model":   "hw.memsize: 1\nhw.ncpu: 1\n",
		"bad ncpu":   "hw.model: X\nhw.memsize: 1\nhw.ncpu: many\n",
		"zero mem":   "hw.model: X\nhw.memsize: 0\nhw.ncpu: 1\n",
		"garbage":    "garbage line\n",
		"empty exit": "",
	}
	for name, out := range cases {
		res := w1OK([]byte(out))
		if name == "empty exit" {
			res = platform.Result{ExitCode: 2}
		}
		f := &platform.FakeRunner{Responses: map[string]platform.Result{
			platform.Key(probe.CmdSysctlHW): res,
			platform.Key(probe.CmdSwVers):   w1OK(w1Fixture(t, "sw_vers.txt")),
		}}
		if _, err := probe.CollectHardware(context.Background(), f); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestRunReadOnlyExitNoStderr(t *testing.T) {
	f := &platform.FakeRunner{Responses: map[string]platform.Result{"vm_stat": {ExitCode: 3}}}
	_, err := probe.RunReadOnly(context.Background(), f, probe.CmdVMStat)
	if err == nil || err.Error() != "probe: vm_stat: exit status 3" {
		t.Fatalf("err = %v", err)
	}
}
