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
	pos     token.Pos // for ordering findings on the same line
	Func    string    // e.g. "os.RemoveAll"; empty for shell findings
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
	cmds := commandNames{
		// Inside package platform itself, Command is unqualified.
		bare: file.Name.Name == "platform" && strings.HasPrefix(filename, "internal/platform/"),
		qual: map[string]bool{},
	}
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if p == platformPath {
			switch {
			case imp.Name == nil:
				cmds.qual["platform"] = true
			case imp.Name.Name == ".":
				cmds.bare = true
			case imp.Name.Name != "_":
				cmds.qual[imp.Name.Name] = true
			}
			continue
		}
		if !watched(p) {
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
	if len(named) == 0 && len(dotted) == 0 && !cmds.any() {
		return nil, nil
	}

	var out []Finding
	report := func(n ast.Node, importPath, name string, r rule) {
		fn := shortName(importPath) + "." + name
		out = append(out, Finding{
			File:    filename,
			Line:    fset.Position(n.Pos()).Line,
			pos:     n.Pos(),
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
						pos:     x.Args[r.nameArg].Pos(),
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
		case *ast.CompositeLit:
			for _, lit := range cmds.literals(x) {
				if v := commandName(lit); v != nil {
					if s, ok := shellLiteral(v); ok {
						out = append(out, Finding{
							File:    filename,
							Line:    fset.Position(v.Pos()).Line,
							pos:     v.Pos(),
							Message: fmt.Sprintf("forbidden shell %q in platform.Command (no shell, ever)", s),
						})
					}
				}
			}
		}
		return true
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].pos < out[j].pos })
	return out, nil
}

// platformPath is the package whose Command type feeds the Runner. A shell
// as Command.Name is never allowed (and never allowlistable).
const platformPath = "macdoc/internal/platform"

// commandNames records how platform.Command can be spelled in one file.
type commandNames struct {
	qual map[string]bool // import names: platform.Command, p.Command
	bare bool            // Command (dot import, or inside package platform)
}

func (c commandNames) any() bool { return c.bare || len(c.qual) > 0 }

// isCommand reports whether a type expression is platform.Command or a
// pointer to it.
func (c commandNames) isCommand(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.StarExpr:
		return c.isCommand(x.X)
	case *ast.ParenExpr:
		return c.isCommand(x.X)
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		return ok && c.qual[id.Name] && x.Sel.Name == "Command"
	case *ast.Ident:
		return c.bare && x.Name == "Command"
	}
	return false
}

// literals returns the platform.Command literals that lit is or directly
// contains with the type elided, e.g. []platform.Command{{Name: "sh"}}.
func (c commandNames) literals(lit *ast.CompositeLit) []*ast.CompositeLit {
	if lit.Type == nil {
		return nil // handled by the enclosing literal
	}
	if c.isCommand(lit.Type) {
		return []*ast.CompositeLit{lit}
	}
	var elem ast.Expr
	switch t := lit.Type.(type) {
	case *ast.ArrayType:
		elem = t.Elt
	case *ast.MapType:
		elem = t.Value
	default:
		return nil
	}
	if !c.isCommand(elem) {
		return nil
	}
	var out []*ast.CompositeLit
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			e = kv.Value
		}
		if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
			e = u.X
		}
		if inner, ok := e.(*ast.CompositeLit); ok && inner.Type == nil {
			out = append(out, inner)
		}
	}
	return out
}

// commandName returns the expression given for Command.Name: the keyed
// Name field, or the first positional element.
func commandName(lit *ast.CompositeLit) ast.Expr {
	for i, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			if i == 0 {
				return e
			}
			return nil
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Name" {
			return kv.Value
		}
	}
	return nil
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
