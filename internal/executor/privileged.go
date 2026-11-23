package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/sandbox"
)

// This file is the only place macdoc runs a command with administrator
// rights, and the only file the forbidden-call guard lets name sudo
// (docs/adr/0014-reclaim-system-cleanup.md, which took over the rules of the
// retired docs/adr/0009-privileged-maintenance-steps.md). The bounds, all
// enforced here and in the planner:
//   - the command is exactly what recipes.SystemArgv builds for a named system
//     location (recipes.SystemArgvOK);
//   - the plan was made by a person, never by Claude;
//   - the person is at a real terminal, and typed the confirmation for this
//     item on its own (Approvals.approves);
//   - the program is a macOS system tool, never one found in the user's PATH;
//   - sudo asks for the password itself, on the terminal: macdoc never reads,
//     keeps or logs it, never uses sudo -S, and ends the sudo session after.

// sudoPath is the system's sudo, run by absolute path.
const sudoPath = "/usr/bin/sudo"

// systemToolDirs are the only folders a privileged program may live in.
var systemToolDirs = []string{"/usr/bin", "/usr/sbin", "/bin", "/sbin"}

// realSystem removes one system item with administrator rights (ADR 0014 §2).
// The command must be exactly what recipes.SystemArgv builds for a path that
// has one of the named layouts; everything else is refused here, whatever the
// caller says. It needs a terminal and no sandbox.
func realSystem(ctx context.Context, argv []string, timeout time.Duration) ([]byte, int, error) {
	if !recipes.SystemArgvOK(argv) {
		return nil, -1, errors.New("command is not a removal of a named system location")
	}
	return runSudo(ctx, argv, timeout)
}

// runSudo runs an argv that the caller has already checked through sudo, which asks for the password on the terminal.
func runSudo(ctx context.Context, argv []string, timeout time.Duration) ([]byte, int, error) {
	if _, active, _ := sandbox.Root(); active {
		return nil, -1, errors.New("privileged commands don't run in sandbox mode")
	}
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		return nil, -1, errors.New("a privileged step needs a real terminal to ask for your password")
	}
	tool, err := systemTool(argv[0])
	if err != nil {
		return nil, -1, err
	}
	defer endSudoSession()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sudoPath, append([]string{"--", tool}, argv[1:]...)...) //nolint:gosec // G204: argv is on the exact allow-list and tool is a system path; no shell.
	var out tailBuffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, &out, os.Stderr
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

// systemTool finds name in the system's own folders only. A program anywhere
// else, even one on PATH that the user owns, is never run as administrator.
func systemTool(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, filepath.Separator) {
		return "", fmt.Errorf("%q isn't a tool name", name)
	}
	for _, dir := range systemToolDirs {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s isn't in a system folder (%s)", name, strings.Join(systemToolDirs, ", "))
}

// endSudoSession makes sudo forget the password it just accepted, so nothing
// run afterwards inherits administrator rights.
func endSudoSession() {
	_ = exec.Command(sudoPath, "-k").Run()
}

// isTerminal reports whether f is a character device, which is what a
// terminal is; a pipe or file is not.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
