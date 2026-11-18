package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
)

func tmInfo(mount string) platform.Result {
	return platform.Result{Stdout: []byte("====================================================\nName          : Backup\nKind          : Local\nMount Point   : " + mount + "\nID            : ABC\n")}
}

// An interrupted backup is reported as advice with an unknown size; complete
// backups and other folders are not.
func TestIncompleteBackups(t *testing.T) {
	key := "tmutil destinationinfo"
	w2WithTools(t, "tmutil")
	mount := t.TempDir()
	db := filepath.Join(mount, "Backups.backupdb", "My Mac")
	for _, d := range []string{"2026-10-01-120000", "2026-11-01-120000.inProgress", "2026-11-02-090000.inProgress", "Latest"} {
		if err := os.MkdirAll(filepath.Join(db, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := w2Run(t, "tm.incomplete_backups", recipes.Env{Runner: w2Fake(map[string]platform.Result{key: tmInfo(mount)})})
	if err != nil || len(fs) != 1 || fs[0].Note != "2 incomplete backups" || fs[0].Bytes.Source != model.SourceUnknown || fs[0].Bytes.Reason == "" {
		t.Fatalf("got %+v, %v", fs, err)
	}

	// Only complete backups, or no backup folder at all: nothing to report.
	clean := t.TempDir()
	if err := os.MkdirAll(filepath.Join(clean, "Backups.backupdb", "My Mac", "2026-10-01-120000"), 0o755); err != nil {
		t.Fatal(err)
	}
	fs, err = w2Run(t, "tm.incomplete_backups", recipes.Env{Runner: w2Fake(map[string]platform.Result{key: tmInfo(clean)})})
	w2WantNone(t, fs, err)
	fs, err = w2Run(t, "tm.incomplete_backups", recipes.Env{Runner: w2Fake(map[string]platform.Result{key: tmInfo(t.TempDir())})})
	w2WantNone(t, fs, err)

	// A relative or parent-walking mount point is ignored; no destinations is not an error.
	for _, mp := range []string{"relative/dir", mount + "/../" + filepath.Base(mount)} {
		fs, err = w2Run(t, "tm.incomplete_backups", recipes.Env{Runner: w2Fake(map[string]platform.Result{key: tmInfo(mp)})})
		w2WantNone(t, fs, err)
	}
	fs, err = w2Run(t, "tm.incomplete_backups", recipes.Env{Runner: w2Fake(map[string]platform.Result{key: {ExitCode: 1, Stderr: []byte("tmutil: No destinations configured.")}})})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "tm.incomplete_backups", recipes.Env{Runner: w2Fake(map[string]platform.Result{key: {ExitCode: 1, Stderr: []byte("boom")}})}); err == nil {
		t.Error("an unexpected failure must not be swallowed")
	}
}
