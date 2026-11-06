package recipes

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// BrewBinary finds the brew executable the way a shell would, WITHOUT
// resolving symlinks: /opt/homebrew/bin/brew is a link into
// /opt/homebrew/Homebrew, and the prefix is two levels above the link, not
// the target. A variable so tests can point it at a fixture.
var BrewBinary = func() (string, error) { return exec.LookPath("brew") }

// brewCacheDirs are the Homebrew download cache folders, relative to home,
// whose top level is part of the fingerprint.
var brewCacheDirs = []string{"Library/Caches/Homebrew", "Library/Caches/Homebrew/downloads"}

// BrewFingerprint is a cheap digest of Homebrew's own state, used to decide
// whether a cached `brew` dry run still describes the machine. It runs no
// brew command: it hashes the names and modification times (Lstat, never
// following links) of every entry directly in <prefix>/Cellar, every
// version folder one level below those, every entry directly in
// <prefix>/Caskroom, the top level of ~/Library/Caches/Homebrew and its
// downloads folder, and the brew binary itself. The prefix is the parent of
// the parent of the brew path (/opt/homebrew, /usr/local). A missing folder
// adds a fixed marker instead of failing. An error means "unknown": the
// caller must run the real command.
func BrewFingerprint(home, brewBin string) (string, error) {
	if home == "" || brewBin == "" {
		return "", errors.New("fingerprint: home and brew path are required")
	}
	prefix := filepath.Dir(filepath.Dir(brewBin))
	h := sha256.New()
	fi, err := os.Lstat(brewBin)
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}
	feed(h, "brew\x00%s\x00%d\n", brewBin, fi.ModTime().UnixNano())
	if fi, err = os.Stat(brewBin); err == nil { // the target of a link: what brew updates
		feed(h, "target\x00%d\n", fi.ModTime().UnixNano())
	}
	if err := hashFolder(h, filepath.Join(prefix, "Cellar"), true); err != nil {
		return "", err
	}
	if err := hashFolder(h, filepath.Join(prefix, "Caskroom"), false); err != nil {
		return "", err
	}
	for _, d := range brewCacheDirs {
		if err := hashFolder(h, filepath.Join(home, d), false); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashFolder adds the entries directly in dir to h. With versions it also
// adds the entries of each subfolder (a link is not followed). A folder that
// does not exist adds a marker; any other read error is returned.
func hashFolder(h hash.Hash, dir string, versions bool) error {
	ents, err := os.ReadDir(dir) // sorted by name; Info() is an Lstat of the entry
	if errors.Is(err, fs.ErrNotExist) {
		feed(h, "missing\x00%s\n", dir)
		return nil
	}
	if err != nil {
		return fmt.Errorf("fingerprint: %w", err)
	}
	feed(h, "dir\x00%s\x00%d\n", dir, len(ents))
	for _, e := range ents {
		fi, err := e.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) { // removed while reading: the next read differs
				continue
			}
			return fmt.Errorf("fingerprint: %w", err)
		}
		feed(h, "%s\x00%d\n", e.Name(), fi.ModTime().UnixNano())
		if versions && e.Type().IsDir() {
			if err := hashFolder(h, filepath.Join(dir, e.Name()), false); err != nil {
				return err
			}
		}
	}
	return nil
}

// feed writes one formatted line to h. A hash never fails to write.
func feed(h hash.Hash, format string, args ...any) {
	_, _ = fmt.Fprintf(h, format, args...)
}
