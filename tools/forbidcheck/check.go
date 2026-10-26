package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// rule describes one forbidden package-level function.
type rule struct {
	reason  string
	isExec  bool // the program name argument is checked for shells
	nameArg int  // index of the program name argument for exec rules
}

const (
	reasonDelete = "only internal/executor may delete"
	reasonMove   = "only internal/executor may delete or move"
	reasonExec   = "only internal/executor may run mutating commands"
)

// forbidden maps import path -> function name -> rule.
var forbidden = map[string]map[string]rule{
	"os": {
		"Remove":       {reason: reasonDelete},
		"RemoveAll":    {reason: reasonDelete},
		"Rename":       {reason: reasonMove},
		"Truncate":     {reason: reasonDelete},
		"StartProcess": {reason: reasonExec, isExec: true, nameArg: 0},
	},
	"syscall": {
		"Unlink":   {reason: reasonDelete},
		"Unlinkat": {reason: reasonDelete},
		"Rmdir":    {reason: reasonDelete},
		"Rename":   {reason: reasonMove},
		"Renameat": {reason: reasonMove},
		"Truncate": {reason: reasonDelete},
		"Exec":     {reason: reasonExec, isExec: true, nameArg: 0},
		"ForkExec": {reason: reasonExec, isExec: true, nameArg: 0},
	},
	"os/exec": {
		"Command":        {reason: reasonExec, isExec: true, nameArg: 0},
		"CommandContext": {reason: reasonExec, isExec: true, nameArg: 1},
	},
}

// unixPrefixes are forbidden name prefixes in golang.org/x/sys/unix
// (Unlink, Unlinkat, Rename, Renameat, Renameat2, RenamexNp, ...).
var unixPrefixes = map[string]string{
	"Unlink":   reasonDelete,
	"Rmdir":    reasonDelete,
	"Rename":   reasonMove,
	"Truncate": reasonDelete,
	"Exec":     reasonExec,
}

const unixPath = "golang.org/x/sys/unix"

// shells may never be the program run by an exec call.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "sudo": true}

func lookup(importPath, name string) (rule, bool) {
	if importPath == unixPath {
		for p, reason := range unixPrefixes {
			if strings.HasPrefix(name, p) {
				return rule{reason: reason, isExec: p == "Exec"}, true
			}
		}
		return rule{}, false
	}
	r, ok := forbidden[importPath][name]
	return r, ok
}

// shortName is the conventional package name used in output and allow.txt.
func shortName(importPath string) string { return path.Base(importPath) }

// Finding is one violation.
type Finding struct {
	File    string // module-relative, slash-separated
	Line    int
	Func    string // e.g. "os.RemoveAll"; empty for shell findings
	Message string
}

func (f Finding) String() string { return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Message) }

// key is the allowlist key for a finding.
func (f Finding) key() string { return f.File + ":" + f.Func }

// checkSource parses one Go file and returns every forbidden reference.
// filename is used for positions and should be module-relative.
func checkSource(filename string, src []byte) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	named := map[string]string{} // local name -> import path
	var dotted []string          // dot-imported paths
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !watched(p) {
			continue
		}
		switch {
		case imp.Name == nil:
			named[shortName(p)] = p
		case imp.Name.Name == ".":
			dotted = append(dotted, p)
		case imp.Name.Name == "_":
		default:
			named[imp.Name.Name] = p
		}
	}
	if len(named) == 0 && len(dotted) == 0 {
		return nil, nil
	}

	var out []Finding
	report := func(n ast.Node, importPath, name string, r rule) {
		fn := shortName(importPath) + "." + name
		out = append(out, Finding{
			File:    filename,
			Line:    fset.Position(n.Pos()).Line,
			Func:    fn,
			Message: fmt.Sprintf("forbidden call %s (%s)", fn, r.reason),
		})
	}
	// resolve returns the forbidden rule an expression refers to, if any.
	resolve := func(e ast.Expr) (string, string, rule, bool) {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return "", "", rule{}, false
			}
			p, ok := named[id.Name]
			if !ok {
				return "", "", rule{}, false
			}
			r, ok := lookup(p, x.Sel.Name)
			return p, x.Sel.Name, r, ok
		case *ast.Ident:
			for _, p := range dotted {
				if r, ok := lookup(p, x.Name); ok {
					return p, x.Name, r, true
				}
			}
		}
		return "", "", rule{}, false
	}

	seen := map[ast.Node]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			p, name, r, ok := resolve(x.Fun)
			if !ok {
				return true
			}
			seen[x.Fun] = true
			report(x, p, name, r)
			if r.isExec && r.nameArg < len(x.Args) {
				if s, ok := shellLiteral(x.Args[r.nameArg]); ok {
					out = append(out, Finding{
						File:    filename,
						Line:    fset.Position(x.Args[r.nameArg].Pos()).Line,
						Message: fmt.Sprintf("forbidden shell %q as command name (no shell, no sudo)", s),
					})
				}
			}
		case *ast.SelectorExpr:
			// A reference that is not a call, e.g. "rm := os.Remove".
			if seen[x] {
				return true
			}
			if p, name, r, ok := resolve(x); ok {
				report(x, p, name, r)
			}
		}
		return true
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out, nil
}

func watched(importPath string) bool {
	_, ok := forbidden[importPath]
	return ok || importPath == unixPath
}

// shellLiteral reports whether e is a string literal naming a shell, either
// bare ("sh") or as a path ("/bin/sh").
func shellLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, shells[path.Base(s)]
}

// Allowlist holds "path:pkg.Func" entries.
type Allowlist map[string]bool

// parseAllowlist reads allow.txt. Blank lines and "#" comments are ignored.
func parseAllowlist(r io.Reader) (Allowlist, error) {
	a := Allowlist{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		file, fn, ok := strings.Cut(line, ":")
		if !ok || file == "" || !strings.Contains(fn, ".") {
			return nil, fmt.Errorf("allow.txt:%d: want path:pkg.Func, got %q", n, line)
		}
		a[line] = true
	}
	return a, sc.Err()
}
