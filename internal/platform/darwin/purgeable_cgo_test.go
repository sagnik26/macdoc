//go:build darwin && cgo

package darwin

import "testing"

func TestPurgeableBytesCgo(t *testing.T) {
	n, ok := PurgeableBytes()
	if !ok {
		t.Fatal("PurgeableBytes not ok on darwin with cgo")
	}
	if n < 0 {
		t.Errorf("PurgeableBytes = %d, want >= 0", n)
	}
	t.Logf("purgeable bytes on this Mac: %d", n)
}
