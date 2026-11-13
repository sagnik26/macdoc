package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/scanner"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// keepFileRoot builds a small tree: an installer and a text file in Downloads,
// an installer in a hidden folder, and an installer in another folder. All
// are far below the scanner's 100 MB keep threshold.
func keepFileRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fstree.Build(t, root,
		fstree.File("Downloads/app.dmg", 4096),
		fstree.File("Downloads/notes.txt", 4096),
		fstree.File("Downloads/.hidden/secret.dmg", 4096),
		fstree.File("Other/app.dmg", 4096),
	)
	return root
}

// A file the callback names is kept whatever its size; others are not.
func TestKeepFileKeepsMatchingSmallFiles(t *testing.T) {
	root := keepFileRoot(t)
	res := scan(t, scanner.Options{
		Root: root,
		KeepFile: func(dir, name string) bool {
			return dir == root+"/Downloads" && strings.HasSuffix(name, ".dmg")
		},
	})
	if n, ok := node(res, root+"/Downloads/app.dmg"); !ok || n.Bytes != alloc(t, root+"/Downloads/app.dmg") {
		t.Errorf("the installer should be kept with its size: %+v, %v", n, ok)
	}
	// Look-alikes and precondition failures: wrong extension, wrong folder,
	// and a file in a hidden folder are all left out.
	for _, p := range []string{"/Downloads/notes.txt", "/Other/app.dmg", "/Downloads/.hidden/secret.dmg"} {
		if _, ok := node(res, root+p); ok {
			t.Errorf("%s should not be kept", p)
		}
	}
}

// Without a callback, small files are never kept, as before.
func TestKeepFileNilKeepsNothingSmall(t *testing.T) {
	root := keepFileRoot(t)
	res := scan(t, scanner.Options{Root: root})
	if _, ok := node(res, root+"/Downloads/app.dmg"); ok {
		t.Error("a 4 KiB file must not be kept without a KeepFile callback")
	}
}
