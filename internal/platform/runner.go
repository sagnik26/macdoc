// Package platform wraps access to macOS commands and APIs.
//
// All external commands used for *reading* system state go through Runner so
// they can be faked in tests. Commands that change anything are run only by
// internal/executor, never through this package.
package platform

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// DefaultTimeout bounds every external command unless overridden.
const DefaultTimeout = 10 * time.Second

// Command is a fixed argv invocation. There is no shell.
type Command struct {
	Name    string
	Args    []string
	Timeout time.Duration // zero means DefaultTimeout
}

// Result is the captured outcome of a command.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner runs read-only external commands.
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// ExecRunner runs commands with os/exec.
type Exec struct{}

// Run executes cmd and returns its output. A non-zero exit is reported in
// Result.ExitCode, not as an error; errors mean the command could not run or
// timed out.
func (Exec) Run(ctx context.Context, cmd Command) (Result, error) {
	timeout := cmd.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tool, err := ResolveTool(cmd.Name)
	if err != nil {
		return Result{}, err
	}
	c := exec.CommandContext(ctx, tool, cmd.Args...) //nolint:gosec // G204: read-only Runner; fixed argv from recipes, no shell (forbidcheck enforces); the path was checked by ResolveTool.
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err = c.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return res, err
}
