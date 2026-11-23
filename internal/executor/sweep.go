package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
)

// Sweep implements planner.Performer for the .DS_Store sweep (ADR 0014 §4).
func (p *performer) Sweep(item model.PlanItem, root string) error {
	return p.do(item, func() (ItemResult, error) { return p.sweep(item, root) })
}

// sweep removes every regular file named exactly .DS_Store below root. It
// re-checks the folder now (a real folder at its canonical path that the sweep
// rules accept) and each file right before removing it. Nothing else is ever
// removed, and nothing is quarantined: the files are tiny and Finder writes
// them again.
func (p *performer) sweep(item model.PlanItem, root string) (ItemResult, error) {
	res := ItemResult{}
	dev, err := p.sweepRoot(item, root)
	if err != nil {
		return res, err
	}
	var removed, failed int
	var bytes int64
	err = recipes.WalkDSStore(p.ctx, root, p.pol.SweepSkips, func(f recipes.SweepFile) error {
		if p.removeDSStore(root, dev, f.Path) {
			removed++
			bytes += f.Bytes
		} else {
			failed++
		}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("the sweep stopped: %w", err)
	}
	switch {
	case removed == 0 && failed == 0:
		return res, skip{"it has no .DS_Store files left"}
	case removed == 0:
		return res, fmt.Errorf("couldn't remove any of %d .DS_Store files", failed)
	}
	res.Files, res.MovedBytes, res.Deleted = removed, bytes, true
	if failed > 0 {
		p.warnings = append(p.warnings, fmt.Sprintf("%s: %d .DS_Store files couldn't be removed", item.Title, failed))
	}
	return res, nil
}

// sweepRoot re-checks the folder to sweep and returns its volume. Anything
// off is a skip: nothing is changed.
func (p *performer) sweepRoot(item model.PlanItem, root string) (uint64, error) {
	if p.plan.Origin == model.OriginClaude {
		return 0, skip{"a sweep is never part of a plan Claude proposed"}
	}
	if item.Target != root {
		return 0, skip{"its folder doesn't match the plan"}
	}
	fi, err := os.Lstat(root)
	switch {
	case err != nil:
		return 0, skipf("it can't be read: %v", err)
	case fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir():
		return 0, skip{"it is no longer a folder"}
	}
	if c, err := safety.Canonicalize(root); err != nil || c != root {
		return 0, skip{"its path changed since the plan"}
	}
	if err := p.pol.CheckSweepRoot(root); err != nil {
		return 0, skipf("the safety rules refuse it: %v", err)
	}
	dev, ok := volumeOf(fi)
	if !ok {
		return 0, skip{"its volume can't be identified"}
	}
	return dev, nil
}

// removeDSStore re-checks one file and removes it. It never removes anything
// that is not a regular file with this exact name on the sweep's own volume
// directly below root, and syscall.Unlink can't remove a folder.
func (p *performer) removeDSStore(root string, dev uint64, path string) bool {
	if filepath.Base(path) != safety.DSStoreName || !safety.Within(root, path) || p.pol.SweepSkips(filepath.Dir(path)) {
		return false
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if d, ok := volumeOf(fi); !ok || d != dev {
		return false
	}
	return syscall.Unlink(path) == nil
}

func volumeOf(fi os.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:gosec // G115: device numbers are non-negative
}
