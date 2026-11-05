package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mutatingCalls are the calls that delete or move. The test below pins
// where each may appear, so a new deletion path in the executor fails the
// build until it's reviewed (Phase 6 done-when: nothing is removed except a
// native command's own output or a purge of a manifest-backed entry).
var mutatingCalls = map[string][]string{
	// The one recursive delete: a quarantine entry, after checkEntryDir.
	"os.RemoveAll": {"removeTree"},
	// Non-recursive removals of macdoc's own entry files and empty folders.
	"os.Remove": {"removeEmptyEntry", "removeEntry", "writeManifest", "RemoveSchedule"},
	// Replacing the manifest atomically inside the entry folder.
	"os.Rename": {"writeManifest"},
	// Moves relative to opened folders, never overwriting.
	"unix.RenameatxNp": {"moveIn", "moveBack", "Restore"},
	"unix.Unlink":      {},
	"unix.Unlinkat":    {},
	"unix.Rmdir":       {},
	"unix.Rename":      {},
	"unix.Renameat":    {},
	"syscall.Unlink":   {},
	"syscall.Rmdir":    {},
	"syscall.Rename":   {},
	"os.Truncate":      {},
	// The only process start: realExec (verified native argv, no shell).
	"exec.CommandContext": {"realExec"},
	"exec.Command":        {},
	"os.StartProcess":     {},
}

func TestMutatingCallSites(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				name := pkg.Name + "." + sel.Sel.Name
				allowed, watched := mutatingCalls[name]
				if watched && !slices.Contains(allowed, fn.Name.Name) {
					t.Errorf("%s: %s in %s, which isn't an allowed site (%v)", fset.Position(sel.Pos()), name, fn.Name.Name, allowed)
				}
				return true
			})
		}
	}
}
