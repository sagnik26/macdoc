package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/probe"
)

// The outputs below were captured from a real Mac (macOS 26, Apple silicon),
// with identifying values replaced.

const topOut = `Load Avg: 3.19, 3.60, 4.00
CPU usage: 22.93% user, 19.95% sys, 57.10% idle
PhysMem: 7454M used (1742M wired, 3291M compressor), 178M unused.
Load Avg: 3.19, 3.60, 4.00
CPU usage: 23.67% user, 6.64% sys, 69.67% idle
PhysMem: 7473M used (1742M wired, 3283M compressor), 158M unused.
`

func TestParseTopCPUUsesTheLastSample(t *testing.T) {
	user, sys, idle, err := probe.ParseTopCPU([]byte(topOut))
	if err != nil || user != 23.67 || sys != 6.64 || idle != 69.67 {
		t.Errorf("got %v %v %v, %v; want the second sample 23.67 6.64 69.67", user, sys, idle, err)
	}
	if _, _, _, err := probe.ParseTopCPU([]byte("nothing useful")); err == nil {
		t.Error("output with no CPU line should be an error")
	}
}

func TestParseLoadAvg(t *testing.T) {
	l1, l5, l15, err := probe.ParseLoadAvg([]byte("{ 3.19 3.60 4.00 }\n"))
	if err != nil || l1 != 3.19 || l5 != 3.60 || l15 != 4.00 {
		t.Errorf("got %v %v %v, %v", l1, l5, l15, err)
	}
	for _, bad := range []string{"", "{ 1.0 }", "{ a b c }"} {
		if _, _, _, err := probe.ParseLoadAvg([]byte(bad)); err == nil {
			t.Errorf("%q should be an error", bad)
		}
	}
}

const netstatOut = `Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
lo0        16384 <Link#1>                        220240     0  967715227   220240     0  967715227     0
lo0        16384 127           localhost         220240     -  967715227   220240     -  967715227     -
en0        1500  <Link#14>   a4:83:e7:00:00:01  3000000     0 1000000000  2000000     0  500000000     0
en0        1500  192.168.1     192.168.1.20       3000000     -  1000000000  2000000     -  500000000     -
en1        1500  <Link#15>   a4:83:e7:00:00:02   100     0       2000      50     0       3000     0
utun3      1380  <Link#16>                        400     0      90000     300     0      80000     0
awdl0      1500  <Link#17>   aa:bb:cc:dd:ee:ff    10     0        500      10     0        500     0
`

// Positive: physical interfaces are summed. Look-alikes: loopback, tunnels,
// AirDrop's interface and the per-address rows are left out, so nothing is
// counted twice.
func TestParseNetstatIBCountsOnlyPhysicalInterfaces(t *testing.T) {
	in, out, err := probe.ParseNetstatIB([]byte(netstatOut))
	if err != nil || in != 1000002000 || out != 500003000 {
		t.Errorf("got in=%d out=%d, %v; want en0 plus en1 only", in, out, err)
	}
	if _, _, err := probe.ParseNetstatIB([]byte("Name Mtu Network\nlo0 16384 <Link#1>\n")); err == nil {
		t.Error("output with no physical interface should be an error")
	}
}

// The rate is the difference between two reads, over the interval; a counter
// that goes backwards is treated as no traffic, never as negative.
func TestCollectNetworkRate(t *testing.T) {
	second := strings.ReplaceAll(netstatOut, "1000000000", "1000001000")
	second = strings.ReplaceAll(second, "500000000", "500000500")
	r := &sequenceRunner{outs: []string{netstatOut, second}}
	rates, err := probe.CollectNetwork(context.Background(), r, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if rates.InBytesPerSec <= 0 || rates.OutBytesPerSec <= 0 || rates.InBytesPerSec < rates.OutBytesPerSec {
		t.Errorf("rates = %+v; want in > out > 0", rates)
	}
	back := &sequenceRunner{outs: []string{second, netstatOut}}
	rates, err = probe.CollectNetwork(context.Background(), back, 10*time.Millisecond)
	if err != nil || rates.InBytesPerSec != 0 || rates.OutBytesPerSec != 0 {
		t.Errorf("a counter that went backwards gave %+v, %v; want zero rates", rates, err)
	}
}

// sequenceRunner returns each output in turn, one per call.
type sequenceRunner struct {
	outs []string
	i    int
}

func (s *sequenceRunner) Run(_ context.Context, _ platform.Command) (platform.Result, error) {
	out := s.outs[min(s.i, len(s.outs)-1)]
	s.i++
	return platform.Result{Stdout: []byte(out)}, nil
}

const iostatOut = `              disk0               disk4               disk5               disk6
    KB/t  tps  MB/s     KB/t  tps  MB/s     KB/t  tps  MB/s     KB/t  tps  MB/s
   17.08  417  6.96   565.21    0  0.00   575.88    0  0.00   602.09    0  0.00
   18.91 1354 25.01     0.00    0  0.00     0.00    0  0.00     0.00    0  0.00
`

func TestParseIostatUsesTheLastRowAndSumsDisks(t *testing.T) {
	io, err := probe.ParseIostat([]byte(iostatOut))
	if err != nil || io.MBPerSec != 25.01 || io.TransfersPerSec != 1354 {
		t.Errorf("got %+v, %v; want the second row, 25.01 MB/s and 1354 tps", io, err)
	}
	if _, err := probe.ParseIostat([]byte("KB/t tps MB/s\n")); err == nil {
		t.Error("a header with no data row should be an error")
	}
}

const pmsetBattery = `Now drawing from 'Battery Power'
 -InternalBattery-0 (id=39190627)	65%; discharging; 13:10 remaining present: true
`

const ioregBattery = `    "CurrentCapacity" = 65
    "ExternalConnected" = No
    "BatteryData" = {"MaxCapacity"=77,"Serial"="SECRET-SERIAL-123","CycleCount"=999}
    "MaxCapacity" = 100
    "Temperature" = 3061
    "DesignCapacity" = 4382
    "CycleCount" = 289
`

func TestPowerFromPmsetAndRegistry(t *testing.T) {
	p := probe.ParsePmsetBatt([]byte(pmsetBattery))
	if !p.Present || !p.OnBattery || p.Percent != 65 || p.State != "discharging" || p.Remaining != "13:10" {
		t.Errorf("pmset = %+v", p)
	}
	p = probe.ApplyBatteryRegistry(p, []byte(ioregBattery))
	if p.CycleCount != 289 || p.HealthPercent != 100 || p.TemperatureC != 30.61 {
		t.Errorf("registry = %+v; want cycles 289, health 100, 30.61 C (the nested BatteryData values must not be read)", p)
	}
}

// Look-alikes and unknowns: a desktop has no battery, "no estimate" has no
// remaining time, and a missing key stays unknown, not zero.
func TestPowerEdges(t *testing.T) {
	desktop := probe.ParsePmsetBatt([]byte("Now drawing from 'AC Power'\n"))
	if desktop.Present || desktop.OnBattery {
		t.Errorf("a desktop should have no battery: %+v", desktop)
	}
	est := probe.ParsePmsetBatt([]byte("Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1)\t80%; discharging; (no estimate) present: true\n"))
	if est.Percent != 80 || est.Remaining != "" {
		t.Errorf("no-estimate line = %+v", est)
	}
	none := probe.ApplyBatteryRegistry(probe.Power{CycleCount: -1, HealthPercent: -1}, []byte(`    "ExternalConnected" = No`))
	if none.CycleCount != -1 || none.HealthPercent != -1 || none.TemperatureC != 0 {
		t.Errorf("missing keys must stay unknown: %+v", none)
	}
}

// A battery's serial number is never read: the whole result, as JSON, must
// not contain it, whatever the registry holds.
func TestPowerNeverCarriesTheSerial(t *testing.T) {
	p := probe.ApplyBatteryRegistry(probe.ParsePmsetBatt([]byte(pmsetBattery)), []byte(ioregBattery))
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRET-SERIAL-123") {
		t.Errorf("the serial leaked into %s", b)
	}
}

func TestCollectCPUWithFakeRunner(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"top -l 2 -n 0 -s 1":      {Stdout: []byte(topOut)},
		"sysctl -n vm.loadavg":    {Stdout: []byte("{ 1.50 2.00 2.50 }")},
		"sysctl -n hw.logicalcpu": {Stdout: []byte("8\n")},
	}}
	c, err := probe.CollectCPU(context.Background(), fake)
	if err != nil || c.LogicalCores != 8 || c.Load1 != 1.5 || c.UsagePercent < 30.3 || c.UsagePercent > 30.4 {
		t.Errorf("cpu = %+v, %v; want 8 cores, load 1.5, about 30.33%% in use", c, err)
	}
}
