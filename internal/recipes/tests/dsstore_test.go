package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
)

func touch(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The detector counts only files named exactly .DS_Store, one finding per
// folder directly in home.
func TestDetectDSStore(t *testing.T) {
	home := t.TempDir()
	body := strings.Repeat("x", 6000)
	for _, f := range []string{
		"Projects/.DS_Store", "Projects/a/b/.DS_Store", "Documents/.DS_Store",
		// look-alikes: not counted
		"Projects/.DS_Store.bak", "Projects/._.DS_Store", "Projects/DS_Store", "Projects/notes.txt",
		// folders the sweep never enters
		"Projects/.git/.DS_Store", "Projects/app/node_modules/x/.DS_Store", "Pics/Photos.photoslibrary/.DS_Store",
		// never swept at all
		"Library/Mail/.DS_Store", ".hidden/.DS_Store", ".DS_Store",
	} {
		touch(t, filepath.Join(home, f), body)
	}
	other := t.TempDir()
	touch(t, filepath.Join(other, ".DS_Store"), body)
	if err := os.Symlink(other, filepath.Join(home, "Linked")); err != nil { // a link is never followed
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, ".DS_Store"), filepath.Join(home, "Projects", "link", ".DS_Store")); err != nil {
		_ = os.MkdirAll(filepath.Join(home, "Projects", "link"), 0o755)
		_ = os.Symlink(filepath.Join(other, ".DS_Store"), filepath.Join(home, "Projects", "link", ".DS_Store"))
	}

	fs, err := recipes.DetectDSStore(context.Background(), recipes.Env{Home: home}, recipes.Recipe{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range fs {
		got[filepath.Base(f.Target)] = f.Note
		if f.Bytes.Value <= 0 {
			t.Errorf("%s: size %d", f.Target, f.Bytes.Value)
		}
	}
	want := map[string]string{"Projects": "2 files", "Documents": "1 file"}
	if len(got) != len(want) || got["Projects"] != want["Projects"] || got["Documents"] != want["Documents"] {
		t.Errorf("findings = %v, want %v", got, want)
	}
}

// Nothing to sweep gives no suggestion; an unreadable home gives none either.
func TestDetectDSStoreNothing(t *testing.T) {
	home := t.TempDir()
	touch(t, filepath.Join(home, "Projects", "a.txt"), "x")
	for _, h := range []string{home, filepath.Join(home, "missing")} {
		fs, err := recipes.DetectDSStore(context.Background(), recipes.Env{Home: h}, recipes.Recipe{})
		if err != nil || len(fs) != 0 {
			t.Errorf("home %s: %v %v", h, fs, err)
		}
	}
}

// The catalog recipe is the one fixed shape the loader accepts.
func TestSweepRecipeShape(t *testing.T) {
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := cat.Get("system.ds_store")
	if !ok {
		t.Fatal("no system.ds_store recipe")
	}
	if r.Method != "sweep" || r.Risk != "CAUTION" || r.Detector != "ds_store" || len(r.Allow) != 0 || r.Native != nil {
		t.Errorf("recipe = %+v", r)
	}
}

// The loader accepts the sweep method only in its one shape; any other recipe
// that tries it (a quarantine-style allow list, another detector, a SAFE
// tier, a command) is rejected.
func TestSweepLoaderRefusals(t *testing.T) {
	good := recipes.Recipe{ID: "system.ds_store", Title: "t", Category: "system", Toolchain: "system",
		Risk: model.RiskCaution, Method: model.MethodSweep, Detector: "ds_store", Restore: model.Restore{Kind: model.RestoreRebuild}, Explain: "x"}
	if err := recipes.Validate(good); err != nil {
		t.Fatalf("the sweep recipe shape is rejected: %v", err)
	}
	for name, mod := range map[string]func(*recipes.Recipe){
		"other detector": func(r *recipes.Recipe) { r.Detector = "children"; r.Params = map[string]string{"dir": "~/Documents/x"} },
		"SAFE":           func(r *recipes.Recipe) { r.Risk = model.RiskSafe },
		"RISKY":          func(r *recipes.Recipe) { r.Risk = model.RiskRisky },
		"allow list":     func(r *recipes.Recipe) { r.Allow = []string{"~/Documents/*"} },
		"command":        func(r *recipes.Recipe) { r.Native = &recipes.Native{Argv: []string{"brew", "cleanup"}} },
		"privileged":     func(r *recipes.Recipe) { r.Privileged = true },
	} {
		r := good
		mod(&r)
		if recipes.Validate(r) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
