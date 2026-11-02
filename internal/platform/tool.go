package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// ResolveTool finds a bare tool name on PATH and checks the file it finds
// (after following symlinks) and the folders it sits in. Anyone other than
// the user or root who can write there could plant a different tool that
// would then run with the user's rights. It returns the path to execute.
// Read-only commands (ExecRunner, ToolInstalled) and the executor's native
// commands all resolve through here.
func ResolveTool(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, filepath.Separator) {
		return "", fmt.Errorf("%q isn't a tool name", name)
	}
	found, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(found)
	if err != nil {
		return "", err
	}
	for _, p := range []string{found, real, filepath.Dir(found), filepath.Dir(real)} {
		if err := trustedPath(p); err != nil {
			return "", fmt.Errorf("%s isn't run: %w", name, err)
		}
	}
	return real, nil
}

// trustedPath refuses a path owned by someone other than the user or root,
// or writable by group or others.
func trustedPath(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New(p + ": owner unknown")
	}
	if uid := int(st.Uid); uid != 0 && uid != os.Getuid() {
		return errors.New(p + " is owned by another user")
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return errors.New(p + " is writable by others")
	}
	// Group-writable is normal for Homebrew (group admin); it's fine when the
	// group is one the user is in, and not when it's a group they're not in.
	if fi.Mode().Perm()&0o020 != 0 && !inGroup(st.Gid) {
		return errors.New(p + " is writable by a group you're not in")
	}
	return nil
}

// inGroup reports whether gid is the user's effective or supplementary group.
func inGroup(gid uint32) bool {
	if uint32(os.Getegid()) == gid { //nolint:gosec // G115: a gid fits in uint32.
		return true
	}
	groups, err := syscall.Getgroups()
	if err != nil {
		return false
	}
	for _, g := range groups {
		if uint32(g) == gid { //nolint:gosec // G115: a gid fits in uint32.
			return true
		}
	}
	return false
}
