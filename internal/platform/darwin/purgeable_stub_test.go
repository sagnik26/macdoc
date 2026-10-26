//go:build !darwin || !cgo

package darwin

import "testing"

func TestPurgeableBytesStub(t *testing.T) {
	if n, ok := PurgeableBytes(); ok || n != 0 {
		t.Errorf("PurgeableBytes = %d, %v; want 0, false", n, ok)
	}
}
