//go:build darwin && cgo

package tests

import (
	"github.com/sagnik26/macdoc/internal/platform/darwin"
	"testing"
)

func TestPurgeableBytesCgo(t *testing.T) {
	n, ok := darwin.PurgeableBytes()
	if !ok {
		t.Fatal("PurgeableBytes not ok on darwin with cgo")
	}
	if n < 0 {
		t.Errorf("PurgeableBytes = %d, want >= 0", n)
	}
	t.Logf("purgeable bytes on this Mac: %d", n)
}
