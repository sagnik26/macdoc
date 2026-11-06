package executor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/config"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/safety"
	"github.com/sagnik26/macdoc/internal/sandbox"
	"github.com/sagnik26/macdoc/internal/store"
)

// EntryResult is the outcome of restoring or purging one quarantine entry.
type EntryResult struct {
	ID       string  `json:"id"`
	Original string  `json:"original"`
	Bytes    int64   `json:"bytes"`
	Outcome  Outcome `json:"outcome"`
	Reason   string  `json:"reason,omitempty"`
}

// ErrNotRestorable means the entry can't be restored as it is.
var ErrNotRestorable = errors.New("can't be restored")

// Restore moves entry id back to where it came from. It refuses if anything
// now exists there, if the path to it changed, or if the entry doesn't
// match macdoc's record. It never overwrites.
func Restore(_ context.Context, cfg Config, id string) (EntryResult, error) {
	if err := cfg.defaults(); err != nil {
		return EntryResult{ID: id}, err
	}
	if _, _, err := sandbox.Root(); err != nil {
		return EntryResult{ID: id}, err
	}
	if err := guardStateDir(cfg.stateDir()); err != nil {
		return EntryResult{ID: id}, fmt.Errorf("nothing was changed: %w", err)
	}
	if !quarantine.ValidID(id) {
		return EntryResult{ID: id}, fmt.Errorf("%q isn't a quarantine ID (they look like Q-91XZ)", id)
	}
	unlock, err := LockQuarantine(cfg.stateDir())
	if err != nil {
		return EntryResult{ID: id}, fmt.Errorf("the quarantine lock can't be taken: %w", err)
	}
	defer unlock()
	rec, err := cfg.Store.Quarantine(id)
	if err != nil {
		return EntryResult{ID: id}, err
	}
	root := cfg.quarantineRoot()
	e := quarantine.Inspect(root, rec, cfg.Now())
	res := EntryResult{ID: id, Original: rec.Original, Bytes: rec.Bytes}
	if !e.Restorable() {
		return res, fmt.Errorf("%s %w: %s", id, ErrNotRestorable, describe(e))
	}
	m := *e.Manifest
	payload := quarantine.PayloadPath(root, m)
	for _, p := range []string{m.Original, payload} {
		if err := sandbox.Check(p); err != nil {
			return res, err
		}
	}
	if c, err := safety.Canonicalize(m.Original); err != nil || c != m.Original {
		return res, fmt.Errorf("%s %w: the path to %s changed (a folder in it may now be a symlink)", id, ErrNotRestorable, m.Original)
	}
	if _, err := os.Lstat(m.Original); err == nil {
		return res, fmt.Errorf("%s %w: something already exists at %s; move it away first", id, ErrNotRestorable, m.Original)
	} else if !errors.Is(err, os.ErrNotExist) {
		return res, err
	}
	parent := filepath.Dir(m.Original)
	pfd, err := openDirStrict(parent)
	if err != nil {
		return res, fmt.Errorf("%s %w: the folder it came from (%s) can't be opened: %w", id, ErrNotRestorable, parent, err)
	}
	defer func() { _ = unix.Close(pfd) }()
	qfd, err := openDirStrict(filepath.Dir(payload))
	if err != nil {
		return res, err
	}
	defer func() { _ = unix.Close(qfd) }()
	if err := sameEntry(qfd, m.Name, m.Fingerprint); err != nil {
		return res, fmt.Errorf("%s %w: the quarantined folder %w", id, ErrNotRestorable, err)
	}
	// Restore puts the folder back somewhere the user already chose to clean,
	// so it is checked again: the policy may have changed (for example a
	// protected folder added in settings) and the recipe may be gone.
	recipe, ok := cfg.Catalog.Get(rec.RecipeID)
	if !ok {
		return res, fmt.Errorf("%s %w: its recipe %q is no longer in the catalog", id, ErrNotRestorable, rec.RecipeID)
	}
	pol, _, err := config.Policy(cfg.Home)
	if err != nil {
		return res, err
	}
	if err := pol.CheckTarget(m.Original, recipe.Allow); err != nil {
		return res, fmt.Errorf("%s %w: %w", id, ErrNotRestorable, err)
	}
	if err := audit.Probe(audit.Path(cfg.StorePath)); err != nil {
		return res, fmt.Errorf("%s: the audit log can't be written, so nothing was changed: %w", id, err)
	}
	if err := unix.RenameatxNp(qfd, m.Name, pfd, m.Name, unix.RENAME_EXCL); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return res, fmt.Errorf("%s %w: something already exists at %s; move it away first", id, ErrNotRestorable, m.Original)
		}
		return res, fmt.Errorf("restore failed: %w", err)
	}
	if err := sameDir(pfd, parent); err != nil {
		if uerr := unix.RenameatxNp(pfd, m.Name, qfd, m.Name, unix.RENAME_EXCL); uerr != nil {
			return res, fmt.Errorf("%w while restoring, and it couldn't be moved back into quarantine: %w", err, uerr)
		}
		return res, fmt.Errorf("%w while restoring; it stays in quarantine", err)
	}
	removeEmptyEntry(quarantine.EntryDir(root, id))
	if err := cfg.Store.SetQuarantineStatus(id, rec.Status, store.QuarantineRestored); err != nil {
		return res, fmt.Errorf("restored, but macdoc's record couldn't be updated: %w", err)
	}
	res.Outcome = OutcomeDone
	n := rec.Bytes
	if err := cfg.audit(audit.Record{Event: audit.Restored, Plan: rec.PlanID, Item: rec.ItemID, Recipe: rec.RecipeID,
		Target: rec.Original, Entry: id, Actual: &n, Result: "ok"}); err != nil {
		res.Reason = "restored, but the audit record couldn't be written: " + err.Error()
	}
	return res, nil
}

// Purge permanently deletes quarantine entries. Only an entry folder that
// macdoc created (a store row and a matching manifest) inside its own
// quarantine folder is removed; anything else is refused and left alone.
// Each entry is handled on its own.
func Purge(_ context.Context, cfg Config, ids []string) ([]EntryResult, error) {
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	if _, _, err := sandbox.Root(); err != nil {
		return nil, err
	}
	if err := guardStateDir(cfg.stateDir()); err != nil {
		return nil, fmt.Errorf("nothing was changed: %w", err)
	}
	root := cfg.quarantineRoot()
	out := make([]EntryResult, 0, len(ids))
	for _, id := range ids {
		out = append(out, purgeAudited(cfg, root, id))
	}
	return out, nil
}

// purgeAudited purges one entry and records the outcome in the audit log.
func purgeAudited(cfg Config, root, id string) EntryResult {
	unlock, err := LockQuarantine(cfg.stateDir())
	if err != nil {
		return EntryResult{ID: id, Outcome: OutcomeSkipped, Reason: "the quarantine lock can't be taken: " + err.Error()}
	}
	r := purgeOne(cfg, root, id)
	unlock()
	rec := audit.Record{Event: audit.Purged, Entry: id, Target: r.Original, Reason: r.Reason}
	if r.Outcome == OutcomeDone {
		n := r.Bytes
		rec.Actual, rec.Result = &n, "ok"
	} else {
		rec.Result = string(r.Outcome)
	}
	if err := cfg.audit(rec); err != nil && r.Reason == "" {
		r.Reason = "the audit record couldn't be written: " + err.Error()
	}
	return r
}

func purgeOne(cfg Config, root, id string) EntryResult {
	res := EntryResult{ID: id, Outcome: OutcomeSkipped}
	if !quarantine.ValidID(id) {
		res.Reason = "not a quarantine ID"
		return res
	}
	rec, err := cfg.Store.Quarantine(id)
	if err != nil {
		res.Reason = "macdoc has no record of it, so it isn't touched"
		return res
	}
	res.Original, res.Bytes = rec.Original, rec.Bytes
	if rec.Status != store.QuarantineDone && rec.Status != store.QuarantinePending {
		res.Reason = "it was already " + string(rec.Status)
		return res
	}
	e := quarantine.Inspect(root, rec, cfg.Now())
	switch e.State {
	case quarantine.Missing:
		// Nothing left to delete; close the record.
		if rec.Status == store.QuarantineDone {
			res.Bytes = 0
			if err := cfg.Store.SetQuarantineStatus(id, rec.Status, store.QuarantinePurged); err != nil {
				res.Reason = err.Error()
				return res
			}
			// A purge that was interrupted after the payload went leaves
			// the manifest behind; clear it (nothing of the item is left).
			removeLeftoverEntry(root, quarantine.EntryDir(root, id), cfg.StageHook)
			res.Outcome, res.Reason = OutcomeDone, "its folder was already gone"
			return res
		}
		res.Reason = describe(e)
		return res
	case quarantine.Damaged:
		res.Reason = describe(e) + "; it isn't touched"
		return res
	case quarantine.Unmoved:
		res.Bytes = 0
	}
	entry := quarantine.EntryDir(root, id)
	if err := checkEntryDir(root, entry); err != nil {
		res.Reason = err.Error() + "; it isn't touched"
		return res
	}
	if err := audit.Probe(audit.Path(cfg.StorePath)); err != nil {
		res.Reason = "the audit log can't be written, so nothing was changed: " + err.Error()
		return res
	}
	if err := removeEntry(entry, cfg.StageHook); err != nil {
		res.Outcome, res.Reason = OutcomeFailed, err.Error()
		return res
	}
	if err := cfg.Store.SetQuarantineStatus(id, rec.Status, store.QuarantinePurged); err != nil {
		res.Outcome, res.Reason = OutcomeFailed, "deleted, but macdoc's record couldn't be updated: "+err.Error()
		return res
	}
	res.Outcome = OutcomeDone
	return res
}

// checkEntryDir verifies an entry folder is a real folder directly inside
// the quarantine root, with a real payload folder, before it's deleted.
func checkEntryDir(root, entry string) error {
	if filepath.Dir(entry) != root {
		return errors.New("it isn't inside macdoc's quarantine folder")
	}
	if c, err := safety.Canonicalize(entry); err != nil || c != entry {
		return errors.New("its path isn't what macdoc expects")
	}
	if err := sandbox.Check(entry); err != nil {
		return err
	}
	for _, p := range []string{entry, filepath.Join(entry, quarantine.Payload)} {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s isn't a plain folder", filepath.Base(p))
		}
	}
	return checkNoOtherVolume(entry)
}

// checkNoOtherVolume refuses an entry with a folder on another volume inside
// it (a disk image or network share mounted there). Such a folder would be
// deleted with its contents (P6-R1 F1: defense in depth for purge).
func checkNoOtherVolume(entry string) error {
	root, err := deviceOf(entry)
	if err != nil {
		return err
	}
	n := 0
	return filepath.WalkDir(entry, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if n++; n > MaxCheckEntries {
			return fmt.Errorf("it has more than %d entries, too many to check", MaxCheckEntries)
		}
		if d.IsDir() && p != entry {
			if dev, err := deviceOf(p); err != nil || dev != root {
				return errors.New("another disk is mounted inside it")
			}
		}
		return nil
	})
}

// removeEntry deletes an entry folder: the payload first, the manifest
// last. A crash part-way then leaves a folder macdoc can still account for
// (the manifest, or only its record), never a payload without its record.
// os.RemoveAll doesn't follow symlinks.
func removeEntry(entry string, stage func(string)) error {
	if err := removeTree(filepath.Join(entry, quarantine.Payload)); err != nil {
		return err
	}
	callHook(stage, "purged-payload")
	if err := os.Remove(filepath.Join(entry, quarantine.ManifestName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("couldn't delete its manifest: %w", err)
	}
	return removeTree(entry)
}

// removeTree removes a folder tree. Read-only folders inside (some tools
// make them) are made writable by their owner first if the first attempt
// fails.
func removeTree(p string) error {
	if err := os.RemoveAll(p); err == nil {
		return nil
	}
	_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(q, 0o700) //nolint:gosec // G302: a folder needs its execute bit to be emptied.
		}
		return nil
	})
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("couldn't delete all of it: %w", err)
	}
	return nil
}

// removeLeftoverEntry removes an entry folder that is left with no payload,
// only if it is a real folder directly inside the quarantine root.
func removeLeftoverEntry(root, entry string, stage func(string)) {
	if filepath.Dir(entry) != root {
		return
	}
	if fi, err := os.Lstat(entry); err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return
	}
	_ = removeEntry(entry, nil)
}

// LockQuarantine takes an exclusive lock on macdoc's quarantine, so a purge
// and a restore (or two purges) of one entry can't interleave (P6-R1 notes).
func LockQuarantine(stateDir string) (func(), error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, "quarantine.lock"), os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // G304: macdoc's own lock file.
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}

func describe(e quarantine.Entry) string {
	switch e.State {
	case quarantine.Unmoved:
		return "apply stopped before moving it, so the original was never touched"
	case quarantine.Missing:
		return "its folder is gone"
	case quarantine.Damaged:
		return "it looks damaged (" + e.Problem + ")"
	}
	return string(e.State)
}
