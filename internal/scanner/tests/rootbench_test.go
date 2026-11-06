package tests

import (
	"context"
	"os"
	"syscall"
	"testing"

	"github.com/sagnik26/macdoc/internal/scanner"
)

// BenchmarkScanRoot scans the real tree named by MACDOC_SCAN_ROOT and reports
// the process CPU time per scan next to the wall time, because a faster
// reader can cut wall time without cutting CPU.
func BenchmarkScanRoot(b *testing.B) {
	root := os.Getenv("MACDOC_SCAN_ROOT")
	if root == "" {
		b.Skip("set MACDOC_SCAN_ROOT to a directory to scan")
	}
	cpu := func() float64 {
		var ru syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
		return float64(ru.Utime.Nano()+ru.Stime.Nano()) / 1e9
	}
	b.ReportAllocs()
	start := cpu()
	for b.Loop() {
		if _, err := scanner.Scan(context.Background(), scanner.Options{Root: root}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric((cpu()-start)/float64(b.N)*1000, "cpu-ms/op")
}
