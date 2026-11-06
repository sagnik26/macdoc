package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/engine"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/probe"
)

const gb = 1_000_000_000

func titles(fs []engine.Finding) string {
	var s []string
	for _, f := range fs {
		s = append(s, string(f.Severity)+":"+f.Title)
	}
	return strings.Join(s, " | ")
}

func TestFindings(t *testing.T) {
	disk := func(free int64) probe.Disk {
		return probe.Disk{ContainerTotalBytes: 245 * gb, ContainerFreeBytes: free}
	}
	tests := []struct {
		name string
		rep  probe.Report
		want []string // substrings, in order
		none bool
	}{
		{name: "healthy", rep: probe.Report{Disk: disk(60 * gb)}, none: true},
		{name: "low disk", rep: probe.Report{Disk: disk(8 * gb)}, want: []string{"warn:Free space is under 10 GB (8.0 GB)"}},
		{name: "critical disk", rep: probe.Report{Disk: disk(3 * gb)}, want: []string{"critical:Only 3.0 GB free"}},
		{name: "swap only", rep: probe.Report{Disk: disk(60 * gb), Swap: probe.Swap{UsedBytes: 3 << 30}},
			want: []string{"warn:3.0 GB of swap in use"}},
		{name: "swap with low disk escalates", rep: probe.Report{Disk: disk(8 * gb), Swap: probe.Swap{UsedBytes: 3 << 30}},
			want: []string{"critical:Low disk (8.0 GB free) while 3.0 GB of swap", "warn:Free space is under 10 GB"}},
		{name: "unknown disk is not reported as low", rep: probe.Report{Swap: probe.Swap{UsedBytes: 3 << 30}},
			want: []string{"warn:3.0 GB of swap in use"}},
		{name: "pressure and heavy app and snapshots and purgeable", rep: probe.Report{
			Disk:         probe.Disk{ContainerTotalBytes: 245 * gb, ContainerFreeBytes: 60 * gb, PurgeableBytes: model.Bytes{Value: 3 * gb, Source: model.SourceMeasured}},
			Memory:       probe.Memory{Pressure: probe.PressureCritical},
			TopProcesses: []probe.ProcessGroup{{Name: "Docker", RSSBytes: 3 << 30}, {Name: "Finder", RSSBytes: 100 << 20}},
			Snapshots:    probe.Snapshots{Count: 2}},
			want: []string{"critical:Memory pressure is critical", "info:Docker is using 3.0 GB", "info:2 local Time Machine", "info:3.0 GB is purgeable"}},
		{name: "swap with normal pressure is informational", rep: probe.Report{Disk: disk(60 * gb), Swap: probe.Swap{UsedBytes: 3 << 30}, Memory: probe.Memory{Pressure: probe.PressureNormal}},
			want: []string{"info:3.0 GB of swap in use, but memory pressure is normal now."}},
		{name: "warn pressure", rep: probe.Report{Memory: probe.Memory{Pressure: probe.PressureWarn}}, want: []string{"warn:Memory pressure is elevated"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.Findings(tt.rep)
			if tt.none {
				if len(got) != 0 {
					t.Fatalf("want no findings, got %s", titles(got))
				}
				return
			}
			s := titles(got)
			pos := 0
			for _, w := range tt.want {
				i := strings.Index(s[pos:], w)
				if i < 0 {
					t.Fatalf("missing %q (in order) in %s", w, s)
				}
				pos += i + len(w)
			}
			if strings.Contains(s, "Finder") {
				t.Errorf("light app reported: %s", s)
			}
		})
	}
}

func TestFindingsSortedBySeverity(t *testing.T) {
	got := engine.Findings(probe.Report{
		Disk:      probe.Disk{ContainerTotalBytes: 245 * gb, ContainerFreeBytes: 2 * gb},
		Snapshots: probe.Snapshots{Count: 1},
		Memory:    probe.Memory{Pressure: probe.PressureWarn},
	})
	rank := map[engine.Severity]int{engine.SeverityCritical: 0, engine.SeverityWarn: 1, engine.SeverityInfo: 2}
	for i := 1; i < len(got); i++ {
		if rank[got[i-1].Severity] > rank[got[i].Severity] {
			t.Fatalf("not sorted: %s", titles(got))
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[string]string{
		engine.DecimalBytes(0):               "0 B",
		engine.DecimalBytes(999):             "999 B",
		engine.DecimalBytes(58_700_000_000):  "58.7 GB",
		engine.DecimalBytes(245_100_000_000): "245 GB",
		engine.DecimalBytes(-1500):           "-1.5 KB",
		engine.BinaryBytes(8 << 30):          "8.0 GB",
		engine.BinaryBytes(1536 << 20):       "1.5 GB",
		engine.BinaryBytes(5 << 40):          "5.0 TB",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestDoctorNeverFails(t *testing.T) {
	res := engine.Doctor(context.Background(), &platform.Fake{})
	if len(res.Report.Warnings) < 4 {
		t.Fatalf("expected a warning per failed probe, got %v", res.Report.Warnings)
	}
	for _, w := range res.Report.Warnings {
		if !strings.Contains(w, ": ") {
			t.Errorf("warning lacks section prefix: %q", w)
		}
	}
}
