package executor

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/safety"
	"github.com/sagnik26/macdoc/internal/sandbox"
	"github.com/sagnik26/macdoc/internal/store"
)

// MaxCheckEntries bounds the content check of one folder. A folder with
// more entries is skipped: macdoc can't vouch for what it didn't look at.
const MaxCheckEntries = 500_000

// callHook runs the test seam Config.StageHook, if set, at one step of a move
// or purge, so a crash or a swap can be simulated there. Production leaves it nil.
func callHook(h func(stage string), stage string) {
	if h != nil {
		h(stage)
	}
}

// quarantine re-checks a target and moves it into a new quarantine entry.
func (p *performer) quarantine(item model.PlanItem, target string) (ItemResult, error) {
	res := ItemResult{}
	r, _ := p.cfg.Catalog.Get(item.RecipeID)
	if err := sandbox.Check(target); err != nil {
		return res, skip{err.Error()}
	}
	fi, err := os.Lstat(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return res, skip{"it no longer exists"}
	case err != nil:
		return res, skipf("it can't be read: %v", err)
	case fi.Mode()&os.ModeSymlink != 0:
		return res, skip{"it is now a symlink"}
	case !fi.IsDir() && !fi.Mode().IsRegular():
		return res, skip{"it isn't a folder or a file"}
	}
	fp := planner.Fingerprint(fi)
	if fp.Dev != item.Fingerprint.Dev || fp.Ino != item.Fingerprint.Ino {
		return res, skip{"it was replaced by something else since the plan"}
	}
	if c, err := safety.Canonicalize(target); err != nil || c != target {
		return res, skip{"its path changed since the plan (a folder above it may now be a symlink)"}
	}
	if err := p.pol.CheckTarget(target, r.Allow); err != nil {
		return res, skipf("the safety rules refuse it: %v", err)
	}
	// Caches change on their own; anything else that changed is skipped.
	measure := fp.MtimeNS != item.Fingerprint.MtimeNS
	if measure && item.Risk != model.RiskSafe {
		return res, skip{"it changed since the plan; make a new plan to include it"}
	}
	bytes, src := item.ExpectedBytes, item.BytesSource
	if fi.IsDir() {
		n, err := checkContents(target, measure)
		if err != nil {
			return res, err
		}
		if measure {
			bytes, src = n, model.SourceMeasured
		}
	} else if measure {
		bytes, src = allocBytes(fi), model.SourceMeasured
	}

	root := p.cfg.quarantineRoot()
	if err := p.ensureRoot(root); err != nil {
		return res, fmt.Errorf("couldn't prepare the quarantine folder: %w", err)
	}
	var rfi unix.Stat_t
	if err := unix.Stat(root, &rfi); err != nil {
		return res, fmt.Errorf("quarantine folder: %w", err)
	}
	if uint64(rfi.Dev) != fp.Dev { //nolint:gosec // G115: same conversion as planner.Fingerprint.
		return res, skip{"it is on another volume than macdoc's quarantine, so it can't be moved there"}
	}
	id, moveErr := p.moveIn(item, target, fp, bytes, src)
	res.QuarantineID = id
	if moveErr != nil {
		return res, moveErr
	}
	res.MovedBytes = bytes
	return res, nil
}

// checkContents walks a folder before it's moved. It refuses one that holds
// a git repository (a .git entry or a bare repo layout) or a .env* file,
// outside vendored package trees, and one too large to check. With measure
// it also totals the allocated bytes.
func checkContents(root string, measure bool) (int64, error) {
	var total int64
	n := 0
	rootDev, err := deviceOf(root)
	if err != nil {
		return 0, skipf("it can't be checked: %v", err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return skipf("part of it can't be read, so it can't be checked: %v", err)
		}
		if n++; n > MaxCheckEntries {
			return skipf("it has more than %d entries, too many to check", MaxCheckEntries)
		}
		if measure {
			if info, err := d.Info(); err == nil {
				total += allocBytes(info)
			}
		}
		if path == root {
			return nil
		}
		rel := strings.TrimPrefix(path, root+"/")
		// A folder on another volume is a mount point: refuse it before
		// anything else, vendored or not (P6-R1 F1).
		if d.IsDir() {
			if dev, err := deviceOf(path); err != nil || dev != rootDev {
				return skipf("another disk is mounted inside it (%s)", rel)
			}
		}
		if vendored(rel) {
			return nil
		}
		name := safety.FoldName(d.Name()) // case-insensitive volumes (P6-R1 F2)
		switch {
		case name == ".git":
			return skipf("it contains a git repository (%s)", rel)
		case strings.HasPrefix(name, ".env") && !d.IsDir():
			return skipf("it contains an environment file (%s)", rel)
		case name == "head" && isRepoHead(d) && isDir(filepath.Join(filepath.Dir(path), "objects")) &&
			isDir(filepath.Join(filepath.Dir(path), "refs")):
			// A bare repository. HEAD may be a symlink (git still reads
			// it, P6-R1 F3).
			return skipf("it contains a git repository (%s)", filepath.Dir(rel))
		case d.IsDir() && vendorDirs[d.Name()] && !measure:
			return filepath.SkipDir // nothing below is checked; skip the walk too
		}
		return nil
	})
	return total, err
}

// isRepoHead reports whether a HEAD entry can be what git reads: a regular
// file, or a symbolic link (core.preferSymlinkRefs).
func isRepoHead(d fs.DirEntry) bool {
	return d.Type().IsRegular() || d.Type()&fs.ModeSymlink != 0
}

// deviceOf returns the device (volume) a path lives on, without following a
// final symlink.
func deviceOf(path string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil //nolint:gosec // G115: a device number, widened for comparison.
}

// vendorDirs hold packages installed by a tool, which may ship their own
// .git or .env files. Their contents aren't checked.
var vendorDirs = map[string]bool{"node_modules": true, "site-packages": true}

// vendored reports whether rel is inside a vendored tree below the root.
func vendored(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, c := range parts[:len(parts)-1] {
		if vendorDirs[c] {
			return true
		}
	}
	return false
}

func isDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

func allocBytes(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return fi.Size()
}

// ensureRoot creates the quarantine folder, private and excluded from
// Spotlight and Time Machine.
func (p *performer) ensureRoot(root string) error {
	// The sandbox check comes first, then the folder is made and checked as
	// a real folder the user owns (P6-R1 F4, F6).
	if err := sandbox.Check(root); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := checkPrivateDir(root); err != nil {
		return err
	}
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // G302: a private folder, owner rwx only.
		return err
	}
	marker := filepath.Join(root, ".metadata_never_index")
	if _, err := os.Lstat(marker); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: inside macdoc's quarantine folder.
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if _, active, _ := sandbox.Root(); !active && p.cfg.realExec {
			if _, code, err := p.cfg.Exec(p.ctx, []string{"tmutil", "addexclusion", root}, timeoutTmutil); err != nil || code != 0 {
				p.warnings = append(p.warnings, "couldn't exclude the quarantine folder from Time Machine")
			}
		}
	}
	return nil
}

// moveIn records a pending entry, moves target into it relative to opened
// parent folders (so a symlink swapped into the path can't redirect the
// move), checks that what arrived is the planned folder, and marks the
// entry done. It returns the entry ID.
func (p *performer) moveIn(item model.PlanItem, target string, fp model.Fingerprint, bytes int64, src model.Source) (string, error) {
	st, root, now := p.cfg.Store, p.cfg.quarantineRoot(), p.cfg.Now()
	id, err := newEntry(st, root)
	if err != nil {
		return "", err
	}
	entry := quarantine.EntryDir(root, id)
	payload := filepath.Join(entry, quarantine.PayloadDir)
	m := quarantine.Manifest{
		SchemaVersion: quarantine.SchemaVersion, ID: id, Plan: p.plan.ID, Item: item.OpportunityID, Recipe: item.RecipeID,
		Title: item.Title, Original: target, Name: filepath.Base(target), Bytes: bytes, BytesSource: src, Fingerprint: fp,
		CreatedAt: now.UTC().Round(0), ExpiresAt: now.Add(p.cfg.QuarantineTTL).UTC().Round(0), Status: quarantine.StatusPending,
	}
	if err := os.Mkdir(payload, 0o700); err != nil {
		removeEmptyEntry(entry)
		return "", err
	}
	if err := st.AddQuarantine(store.QuarantineRecord{ID: id, PlanID: m.Plan, ItemID: m.Item, RecipeID: m.Recipe,
		Original: m.Original, Bytes: m.Bytes, CreatedAt: m.CreatedAt, ExpiresAt: m.ExpiresAt}); err != nil {
		removeEmptyEntry(entry)
		return "", err
	}
	cancel := func(why error) (string, error) {
		removeEmptyEntry(entry)
		_ = st.SetQuarantineStatus(id, store.QuarantinePending, store.QuarantineCanceled)
		return "", why
	}
	if err := writeManifest(entry, m, true); err != nil {
		return cancel(err)
	}
	callHook(p.cfg.StageHook, "pending")

	parent, base := filepath.Dir(target), filepath.Base(target)
	pfd, err := openDirStrict(parent)
	if err != nil {
		return cancel(skipf("its folder can't be opened: %v", err))
	}
	defer func() { _ = unix.Close(pfd) }()
	callHook(p.cfg.StageHook, "opened")
	// Before the move too: the folder we hold must still be the canonical
	// parent path (P6-R1 F5).
	if err := sameDir(pfd, parent); err != nil {
		return cancel(skip{err.Error()})
	}
	qfd, err := openDirStrict(payload)
	if err != nil {
		return cancel(err)
	}
	defer func() { _ = unix.Close(qfd) }()
	if err := sameEntry(pfd, base, fp); err != nil {
		return cancel(skip{err.Error()})
	}
	if err := unix.RenameatxNp(pfd, base, qfd, base, unix.RENAME_EXCL); err != nil {
		return cancel(fmt.Errorf("the move failed: %w", err))
	}
	callHook(p.cfg.StageHook, "moved")
	// What arrived must be the planned folder, and the folder it left must
	// still be the one at the canonical parent path. Otherwise put it back.
	if err := sameEntry(qfd, base, fp); err != nil {
		return id, p.moveBack(qfd, pfd, base, id, entry, err)
	}
	if err := sameDir(pfd, parent); err != nil {
		return id, p.moveBack(qfd, pfd, base, id, entry, err)
	}
	m.Status = quarantine.StatusDone
	if err := writeManifest(entry, m, false); err != nil {
		p.warnings = append(p.warnings, fmt.Sprintf("%s: moved, but its manifest couldn't be updated: %v (it is still restorable)", id, err))
		return id, nil
	}
	callHook(p.cfg.StageHook, "done")
	if err := st.SetQuarantineStatus(id, store.QuarantinePending, store.QuarantineDone); err != nil {
		p.warnings = append(p.warnings, fmt.Sprintf("%s: moved, but macdoc's record couldn't be updated: %v (it is still restorable)", id, err))
	}
	return id, nil
}

// moveBack undoes a move that failed its after-check.
func (p *performer) moveBack(qfd, pfd int, base, id, entry string, why error) error {
	if err := unix.RenameatxNp(qfd, base, pfd, base, unix.RENAME_EXCL); err != nil {
		return fmt.Errorf("%w; it couldn't be moved back and stays in quarantine as %s: %w", why, id, err)
	}
	removeEmptyEntry(entry)
	_ = p.cfg.Store.SetQuarantineStatus(id, store.QuarantinePending, store.QuarantineCanceled)
	return fmt.Errorf("%w; it was moved back", why)
}

// openDirStrict opens an absolute, canonical folder path one component at a
// time, and refuses a symlink in any component, not just the last one. A
// swapped ancestor (a symlink where a folder was) makes it fail, instead of
// being followed (P6-R1 F5).
func openDirStrict(path string) (int, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, c := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if c == "" {
			continue
		}
		next, err := unix.Openat(fd, c, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

// sameEntry checks that name in the folder dirfd is the fingerprinted
// entry and not a symlink.
func sameEntry(dirfd int, name string, fp model.Fingerprint) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("it can't be found: %w", err)
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return errors.New("it is now a symlink")
	}
	if uint64(st.Dev) != fp.Dev || st.Ino != fp.Ino { //nolint:gosec // G115: same conversion as planner.Fingerprint.
		return errors.New("it was replaced by something else")
	}
	return nil
}

// sameDir checks that the folder at path, opened again without following
// any symlink, is the one dirfd has open. (Re-resolving the path with a
// plain Lstat would follow a swapped ancestor and always match.)
func sameDir(dirfd int, path string) error {
	var a unix.Stat_t
	if err := unix.Fstat(dirfd, &a); err != nil {
		return err
	}
	fd, err := openDirStrict(path)
	if err != nil {
		return fmt.Errorf("its folder changed during the move: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	var b unix.Stat_t
	if err := unix.Fstat(fd, &b); err != nil {
		return err
	}
	if a.Dev != b.Dev || a.Ino != b.Ino {
		return errors.New("its folder changed during the move")
	}
	return nil
}

// writeManifest writes the manifest: create-only for the first write, then
// replaced atomically.
func writeManifest(entry string, m quarantine.Manifest, first bool) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	final := filepath.Join(entry, quarantine.ManifestName)
	if first {
		return writeNew(final, b)
	}
	tmp := final + ".tmp"
	_ = os.Remove(tmp)
	if err := writeNew(tmp, b); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func writeNew(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: inside macdoc's quarantine folder.
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// removeEmptyEntry removes an entry's manifest and its (empty) folders. It
// never removes anything recursively: a payload that isn't empty stays.
func removeEmptyEntry(entry string) {
	_ = os.Remove(filepath.Join(entry, quarantine.ManifestName+".tmp"))
	_ = os.Remove(filepath.Join(entry, quarantine.ManifestName))
	_ = os.Remove(filepath.Join(entry, quarantine.PayloadDir))
	_ = os.Remove(entry)
}

const idChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// newEntry creates a new, unused entry folder and returns its ID.
func newEntry(st *store.Store, root string) (string, error) {
	for range 32 {
		b := make([]byte, 4)
		for i := range b {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(idChars))))
			if err != nil {
				return "", err
			}
			b[i] = idChars[n.Int64()]
		}
		id := "Q-" + string(b)
		if taken, err := st.HasQuarantine(id); err != nil {
			return "", err
		} else if taken {
			continue
		}
		if err := os.Mkdir(quarantine.EntryDir(root, id), 0o700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return "", err
		}
		return id, nil
	}
	return "", errors.New("couldn't find an unused quarantine ID")
}
