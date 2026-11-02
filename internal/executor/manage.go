package executor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"macdoc/internal/audit"
	"macdoc/internal/quarantine"
	"macdoc/internal/safety"
	"macdoc/internal/sandbox"
	"macdoc/internal/store"
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
	if err := cfg.audit(audit.Record{Event: audit.QuarantineRestored, Plan: rec.PlanID, Item: rec.ItemID, Recipe: rec.RecipeID,
		Target: rec.Original, QuarantineID: id, ActualBytes: &n, Result: "ok"}); err != nil {
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
	r := purgeOne(cfg, root, id)
	rec := audit.Record{Event: audit.QuarantinePurged, QuarantineID: id, Target: r.Original, Reason: r.Reason}
	if r.Outcome == OutcomeDone {
		n := r.Bytes
		rec.ActualBytes, rec.Result = &n, "ok"
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
	case quarantine.StateMissing:
		// Nothing left to delete; close the record.
		if rec.Status == store.QuarantineDone {
			res.Bytes = 0
			if err := cfg.Store.SetQuarantineStatus(id, rec.Status, store.QuarantinePurged); err != nil {
				res.Reason = err.Error()
				return res
			}
			res.Outcome, res.Reason = OutcomeDone, "its folder was already gone"
			return res
		}
		res.Reason = describe(e)
		return res
	case quarantine.StateDamaged:
		res.Reason = describe(e) + "; it isn't touched"
		return res
	case quarantine.StateNotMoved:
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
	if err := removeEntry(entry); err != nil {
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
	for _, p := range []string{entry, filepath.Join(entry, quarantine.PayloadDir)} {
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

// removeEntry deletes an entry folder. os.RemoveAll doesn't follow
// symlinks. Read-only folders inside (some tools make them) are made
// writable by their owner first if the first attempt fails.
func removeEntry(entry string) error {
	if err := os.RemoveAll(entry); err == nil {
		return nil
	}
	_ = filepath.WalkDir(entry, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700) //nolint:gosec // G302: a folder needs its execute bit to be emptied.
		}
		return nil
	})
	if err := os.RemoveAll(entry); err != nil {
		return fmt.Errorf("couldn't delete all of it: %w", err)
	}
	return nil
}

func describe(e quarantine.Entry) string {
	switch e.State {
	case quarantine.StateNotMoved:
		return "apply stopped before moving it, so the original was never touched"
	case quarantine.StateMissing:
		return "its folder is gone"
	case quarantine.StateDamaged:
		return "it looks damaged (" + e.Problem + ")"
	}
	return string(e.State)
}
