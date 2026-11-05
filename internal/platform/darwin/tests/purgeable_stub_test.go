//go:build !darwin || !cgo

package tests

import (
	"macdoc/internal/platform/darwin"
	"testing"
)

func TestPurgeableBytesStub(t *testing.T) {
	if n, ok := darwin.PurgeableBytes(); ok || n != 0 {
		t.Errorf("PurgeableBytes = %d, %v; want 0, false", n, ok)
	}
}
