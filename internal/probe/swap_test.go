package probe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"macdoc/internal/platform"
)

func TestParseSwapUsageGolden(t *testing.T) {
	s, err := ParseSwapUsage(w1Fixture(t, "swapusage.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// 5120.00M, 3838.06M, 1281.94M with binary multipliers, rounded.
	want := Swap{TotalBytes: 5120 << 20, UsedBytes: 4024497603, FreeBytes: 1344211517, Encrypted: true}
	if s != want {
		t.Fatalf("got %+v, want %+v", s, want)
	}
}

func TestParseSwapUsageUnits(t *testing.T) {
	s, err := ParseSwapUsage(w1Fixture(t, "w1_swapusage_gm.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Swap{TotalBytes: 2 << 30, UsedBytes: 512 << 20, FreeBytes: 3 << 29, Encrypted: true}); s != want {
		t.Fatalf("G/M: got %+v, want %+v", s, want)
	}
	s, err = ParseSwapUsage(w1Fixture(t, "w1_swapusage_k_plain.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Swap{TotalBytes: 1024 << 10, UsedBytes: 262656, FreeBytes: 785920}); s != want {
		t.Fatalf("K/plain: got %+v, want %+v", s, want)
	}
	s, err = ParseSwapUsage([]byte("total = 0.00M  used = 0.00M  free = 0.00M  (encrypted)"))
	if err != nil || s != (Swap{Encrypted: true}) {
		t.Fatalf("zero: got %+v %v", s, err)
	}
	s, err = ParseSwapUsage([]byte("total = 100  used = 40  free = 60"))
	if err != nil || s != (Swap{TotalBytes: 100, UsedBytes: 40, FreeBytes: 60}) {
		t.Fatalf("no unit: got %+v %v", s, err)
	}
}

func TestParseSwapUsageBad(t *testing.T) {
	for name, in := range map[string]string{
		"empty":     "",
		"garbage":   string(w1Fixture(t, "w1_garbage.txt")),
		"no used":   "total = 1.00M  free = 1.00M",
		"no equals": "total 1.00M used = 0.00M free = 1.00M",
		"truncated": "total = 1.00M used =",
		"bad num":   "total = abcM used = 0.00M free = 1.00M",
		"negative":  "total = -1.00M used = 0.00M free = 1.00M",
		"unit only": "total = M used = 0.00M free = 1.00M",
		"huge":      "total = 99999999999999999999G used = 0.00M free = 1.00M",
	} {
		_, err := ParseSwapUsage([]byte(in))
		if err == nil {
			t.Errorf("%s: want error", name)
		} else if !strings.HasPrefix(err.Error(), "probe: vm.swapusage: ") {
			t.Errorf("%s: error %q lacks context", name, err)
		}
	}
}

func TestParseSwapSizeEmpty(t *testing.T) {
	if _, err := parseSwapSize(""); err == nil {
		t.Fatal("want error")
	}
}

func TestCollectSwap(t *testing.T) {
	f := &platform.FakeRunner{Responses: map[string]platform.Result{
		"sysctl -n vm.swapusage": w1OK(w1Fixture(t, "swapusage.txt")),
	}}
	s, err := CollectSwap(context.Background(), f)
	if err != nil || s.TotalBytes != 5120<<20 || !s.Encrypted {
		t.Fatalf("got %+v %v", s, err)
	}
	w1AssertCalls(t, f, platform.Command{Name: "sysctl", Args: []string{"-n", "vm.swapusage"}})
}

func TestCollectSwapError(t *testing.T) {
	boom := errors.New("timeout")
	f := &platform.FakeRunner{Errors: map[string]error{"sysctl -n vm.swapusage": boom}}
	_, err := CollectSwap(context.Background(), f)
	if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "probe: sysctl -n vm.swapusage: ") {
		t.Fatalf("err = %v", err)
	}
	w1AssertCalls(t, f, cmdSwapUsage)
}
