package tests

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"macdoc/internal/platform"
)

// w1Fixture reads a file from testdata.
func w1Fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// w1OK is a successful command result with the given stdout.
func w1OK(stdout []byte) platform.Result { return platform.Result{Stdout: stdout} }

// w1AssertCalls checks that exactly want ran, in order.
func w1AssertCalls(t *testing.T, f *platform.FakeRunner, want ...platform.Command) {
	t.Helper()
	got := make([]string, len(f.Calls))
	for i, c := range f.Calls {
		got[i] = platform.Key(c)
		if c.Timeout != 0 {
			t.Errorf("call %q: timeout %v, want default (0)", got[i], c.Timeout)
		}
	}
	exp := make([]string, len(want))
	for i, c := range want {
		exp[i] = platform.Key(c)
	}
	if !reflect.DeepEqual(got, exp) {
		t.Fatalf("calls = %q, want %q", got, exp)
	}
}
