package executor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/sagnik26/macdoc/internal/platform"
)

// ResolveTool finds a bare tool name on PATH and returns the path to run,
// after the trust check in platform.ResolveTool.
func ResolveTool(name string) (string, error) { return platform.ResolveTool(name) }

const timeoutTmutil = 30 * time.Second

// maxCapture is how much of a command's output is kept (the tail).
const maxCapture = 64 << 10

// realExec runs argv directly (no shell) with a timeout, capturing the tail
// of its combined output. Stdin is empty, so a tool that prompts gets EOF
// instead of waiting.
func realExec(ctx context.Context, argv []string, timeout time.Duration) ([]byte, int, error) {
	if len(argv) == 0 {
		return nil, -1, errors.New("empty command")
	}
	tool, err := ResolveTool(argv[0])
	if err != nil {
		return nil, -1, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, argv[1:]...) //nolint:gosec // G204: fixed argv from a verified plan's recipe, run by the path ResolveTool checked; no shell.
	var out tailBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	if ctx.Err() != nil {
		return out.Bytes(), -1, fmt.Errorf("timed out after %s", timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out.Bytes(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return out.Bytes(), -1, err
	}
	return out.Bytes(), 0, nil
}

// tailBuffer keeps the last maxCapture bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > maxCapture {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-maxCapture:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) Bytes() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.buf...)
}
