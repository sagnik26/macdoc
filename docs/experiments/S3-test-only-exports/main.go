// Command exports lists exported package-level identifiers (and methods) of a
// module whose only users outside their own package are test packages.
package main

import (
	"fmt"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func isTest(path string) bool {
	return strings.Contains(path, "/tests") || strings.HasSuffix(path, "_test") || strings.Contains(path, "/testutil")
}

func main() {
	dir := os.Args[1]
	mod := os.Args[2]
	cfg := &packages.Config{Dir: dir, Tests: true, Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedImports | packages.NeedDeps | packages.NeedFiles}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		panic(err)
	}
	type key struct{ pkg, name, kind string }
	defs := map[types.Object]key{}
	extNonTest := map[types.Object]bool{}
	extTest := map[types.Object]bool{}
	sameOnly := map[types.Object]bool{}
	for _, p := range pkgs {
		if p.Types == nil || !strings.HasPrefix(p.PkgPath, mod) {
			continue
		}
		if isTest(p.PkgPath) {
			continue
		}
		sc := p.Types.Scope()
		for _, n := range sc.Names() {
			o := sc.Lookup(n)
			if !o.Exported() {
				continue
			}
			kind := "other"
			switch o.(type) {
			case *types.Func:
				kind = "func"
			case *types.TypeName:
				kind = "type"
			case *types.Var:
				kind = "var"
			case *types.Const:
				kind = "const"
			}
			defs[o] = key{strings.TrimPrefix(p.PkgPath, mod+"/"), n, kind}
			if tn, ok := o.(*types.TypeName); ok {
				if nt, ok := tn.Type().(*types.Named); ok {
					for i := 0; i < nt.NumMethods(); i++ {
						m := nt.Method(i)
						if m.Exported() {
							defs[m] = key{strings.TrimPrefix(p.PkgPath, mod+"/"), n + "." + m.Name(), "method"}
						}
					}
				}
			}
		}
	}
	// objects from different load variants (pkg vs pkg [pkg.test]) are distinct;
	// match by package path + qualified name.
	byName := map[string]types.Object{}
	idx := func(o types.Object) string { return o.Pkg().Path() + "|" + qual(o) }
	for o := range defs {
		byName[idx(o)] = o
	}
	for _, p := range pkgs {
		if p.TypesInfo == nil || !strings.HasPrefix(p.PkgPath, mod) {
			continue
		}
		user := strings.TrimSuffix(strings.Split(p.PkgPath, " ")[0], ".test")
		for _, o := range p.TypesInfo.Uses {
			if o.Pkg() == nil || !strings.HasPrefix(o.Pkg().Path(), mod) {
				continue
			}
			d, ok := byName[idx(o)]
			if !ok {
				continue
			}
			if o.Pkg().Path() == user {
				sameOnly[d] = true
				continue
			}
			if isTest(user) {
				extTest[d] = true
			} else {
				extNonTest[d] = true
			}
		}
	}
	type row struct {
		k      key
		tested bool
	}
	var rows []row
	for o, k := range defs {
		if extNonTest[o] {
			continue
		}
		rows = append(rows, row{k, extTest[o]})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].k, rows[j].k
		if a.pkg != b.pkg {
			return a.pkg < b.pkg
		}
		return a.name < b.name
	})
	totalByPkg := map[string]int{}
	testOnlyByPkg := map[string]int{}
	for _, k := range defs {
		totalByPkg[k.pkg]++
	}
	for _, r := range rows {
		if r.tested {
			testOnlyByPkg[r.k.pkg]++
		}
	}
	fmt.Println("pkg\texported\tused_only_by_tests")
	var pk []string
	for p := range totalByPkg {
		pk = append(pk, p)
	}
	sort.Strings(pk)
	t1, t2 := 0, 0
	for _, p := range pk {
		fmt.Printf("%s\t%d\t%d\n", p, totalByPkg[p], testOnlyByPkg[p])
		t1 += totalByPkg[p]
		t2 += testOnlyByPkg[p]
	}
	fmt.Printf("TOTAL\t%d\t%d\n", t1, t2)
	fmt.Println("--- detail")
	for _, r := range rows {
		if r.tested {
			fmt.Printf("%s %s.%s\n", r.k.kind, r.k.pkg, r.k.name)
		}
	}
}

func qual(o types.Object) string {
	if f, ok := o.(*types.Func); ok {
		if sig, ok := f.Type().(*types.Signature); ok && sig.Recv() != nil {
			t := sig.Recv().Type()
			if p, ok := t.(*types.Pointer); ok {
				t = p.Elem()
			}
			if n, ok := t.(*types.Named); ok {
				return n.Obj().Name() + "." + f.Name()
			}
		}
	}
	return o.Name()
}
