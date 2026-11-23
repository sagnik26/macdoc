package tests

import (
	"bytes"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/sandbox"
)

// syncBuf is a buffer a test can read while a command is still writing to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// sandboxFor puts the test under MACDOC_SANDBOX, as every removal test does.
func sandboxFor(t *testing.T, home string) {
	t.Helper()
	t.Setenv(sandbox.Env, filepath.Dir(home))
}

// termOn makes the CLI believe it is (or is not) at a real terminal.
func termOn(t *testing.T, on bool) {
	t.Helper()
	old := cli.RealTTY
	cli.RealTTY = func(io.Reader, io.Writer) bool { return on }
	t.Cleanup(func() { cli.RealTTY = old })
}
