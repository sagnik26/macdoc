// Command renamer applies a batch of semantic renames to a Go module.
//
// Each rename names an object by package path and a dotted name ("Name",
// "Type.Method" or "Type.Field") and gives its new name. All uses and
// definitions, including those in test packages, are rewritten in one pass. A
// rename is skipped, with a reason, if the new name would collide where the old
// one is defined or used.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type rename struct {
	Pkg  string `json:"pkg"`  // import path
	Name string `json:"name"` // Name, Type.Method or Type.Field
	New  string `json:"new"`
}

type target struct {
	r   rename
	pos token.Position
}

type edit struct {
	file string
	off  int
	old  string
	new  string
}

func main() {
	dir, mapFile, apply := os.Args[1], os.Args[2], len(os.Args) > 3 && os.Args[3] == "apply"
	var rs []rename
	b, err := os.ReadFile(mapFile)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, &rs); err != nil {
		panic(err)
	}
	cfg := &packages.Config{Dir: dir, Tests: true, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		panic(err)
	}
	fset := pkgs[0].Fset

	// definition position -> new name, for every renamed object
	var targets []target
	skipped := map[string]string{}
	key := func(r rename) string { return r.Pkg + "." + r.Name }

	// find objects in the non-test variant of each package
	for _, r := range rs {
		var obj types.Object
		for _, p := range pkgs {
			if p.PkgPath != r.Pkg || p.Types == nil || strings.HasSuffix(p.ID, ".test]") {
				continue
			}
			parts := strings.Split(r.Name, ".")
			switch len(parts) {
			case 1:
				obj = p.Types.Scope().Lookup(parts[0])
			case 2:
				if tn, ok := p.Types.Scope().Lookup(parts[0]).(*types.TypeName); ok {
					o, _, _ := types.LookupFieldOrMethod(tn.Type(), true, p.Types, parts[1])
					obj = o
					// A field with no json or yaml tag is serialized under its Go name,
					// so renaming it would change a file or wire format.
					if v, ok := o.(*types.Var); ok && v.IsField() {
						if st, ok := tn.Type().Underlying().(*types.Struct); ok && !tagged(st, v) {
							skipped[key(r)] = "field has no json or yaml tag"
							obj = nil
						}
					}
					// a collision with an existing member of the type
					if o2, _, _ := types.LookupFieldOrMethod(tn.Type(), true, p.Types, r.New); o2 != nil {
						skipped[key(r)] = "collides with member " + r.New
						obj = nil
					}
				}
			}
			if obj != nil && len(parts) == 1 {
				if p.Types.Scope().Lookup(r.New) != nil {
					skipped[key(r)] = "collides with package-level " + r.New
					obj = nil
				}
			}
			break
		}
		if obj == nil {
			if _, ok := skipped[key(r)]; !ok {
				skipped[key(r)] = "not found"
			}
			continue
		}
		targets = append(targets, target{r, fset.Position(obj.Pos())})
	}
	byPos := map[string]rename{}
	for _, t := range targets {
		byPos[fmt.Sprintf("%s:%d", t.pos.Filename, t.pos.Offset)] = t.r
	}

	// collect every identifier whose object was defined at a target position
	edits := map[string]edit{}
	bad := map[string]string{} // rename key -> collision at a use site
	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		visit := func(id *ast.Ident, obj types.Object) {
			if obj == nil || !obj.Pos().IsValid() {
				return
			}
			dp := fset.Position(obj.Pos())
			r, ok := byPos[fmt.Sprintf("%s:%d", dp.Filename, dp.Offset)]
			if !ok || id.Name != lastPart(r.Name) {
				return
			}
			pos := fset.Position(id.Pos())
			// the new name must not already be visible where the identifier is used
			if sc := p.Types.Scope().Innermost(id.Pos()); sc != nil && !isSelectorName(p, id) {
				if _, o := sc.LookupParent(r.New, id.Pos()); o != nil && o != obj {
					bad[key(r)] = fmt.Sprintf("%s is already visible at %s:%d", r.New, pos.Filename, pos.Line)
				}
			}
			edits[fmt.Sprintf("%s:%d", pos.Filename, pos.Offset)] = edit{pos.Filename, pos.Offset, id.Name, r.New}
		}
		for id, obj := range p.TypesInfo.Defs {
			visit(id, obj)
		}
		for id, obj := range p.TypesInfo.Uses {
			visit(id, obj)
		}
	}
	for k, why := range bad {
		skipped[k] = why
	}
	// drop edits of skipped renames
	final := map[string][]edit{}
	n := 0
	for _, e := range edits {
		skip := false
		for _, t := range targets {
			if _, s := skipped[key(t.r)]; s && lastPart(t.r.Name) == e.old && t.r.New == e.new {
				skip = true
			}
		}
		if !skip {
			final[e.file] = append(final[e.file], e)
			n++
		}
	}
	applied := len(targets) - countSkipped(targets, skipped, key)
	fmt.Printf("renames requested %d, applicable %d, edits %d in %d files\n", len(rs), applied, n, len(final))
	var keys []string
	for k := range skipped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("SKIP %s: %s\n", k, skipped[k])
	}
	if !apply {
		return
	}
	for f, es := range final {
		src, err := os.ReadFile(f)
		if err != nil {
			panic(err)
		}
		sort.Slice(es, func(i, j int) bool { return es[i].off > es[j].off })
		for _, e := range es {
			if string(src[e.off:e.off+len(e.old)]) != e.old {
				panic("stale offset in " + f)
			}
			src = append(src[:e.off], append([]byte(e.new), src[e.off+len(e.old):]...)...)
		}
		if err := os.WriteFile(f, src, 0o644); err != nil {
			panic(err)
		}
	}
}

func lastPart(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

func countSkipped(ts []target, sk map[string]string, key func(rename) string) int {
	n := 0
	for _, t := range ts {
		if _, ok := sk[key(t.r)]; ok {
			n++
		}
	}
	return n
}

// isSelectorName reports whether id is the Sel of a selector expression, where
// scope lookup is not how the name is resolved.
func isSelectorName(p *packages.Package, id *ast.Ident) bool {
	for _, f := range p.Syntax {
		if f.Pos() > id.Pos() || id.End() > f.End() {
			continue
		}
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			if se, ok := n.(*ast.SelectorExpr); ok && se.Sel == id {
				found = true
			}
			return !found
		})
		return found
	}
	return false
}

// tagged reports whether field v of st carries a json or yaml tag.
func tagged(st *types.Struct, v *types.Var) bool {
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i) == v {
			t := st.Tag(i)
			return strings.Contains(t, `json:"`) || strings.Contains(t, `yaml:"`)
		}
	}
	return false
}
