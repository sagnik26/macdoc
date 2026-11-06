//go:build !darwin || !cgo

package tests

import (
	"testing"

	"github.com/sagnik26/macdoc/internal/platform/darwin"
)

func TestPurgeableBytesStub(t *testing.T) {
	if n, ok := darwin.PurgeableBytes(); ok || n != 0 {
		t.Errorf("PurgeableBytes = %d, %v; want 0, false", n, ok)
	}
}
