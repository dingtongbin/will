// Package index writes and reads the text index of a loaded module: packages,
// files, imports, declarations, methods, interface satisfaction, and C imports.
// The index comes only from the compiler, is complete, and is deterministic.
package index

import (
	"sort"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/pkg"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/willc"
)

// Build renders the index of one loaded module.
func Build(b *pkg.Build) []byte {
	idx := &builder{b: b, ifaceKey: map[string]string{}}
	idx.run()
	return []byte(strings.Join(idx.lines, "\n") + "\n")
}

type builder struct {
	b        *pkg.Build
	lines    []string
	ifaceKey map[string]string
}

func (x *builder) line(parts ...string) {
	x.lines = append(x.lines, strings.Join(parts, " "))
}

func (x *builder) run() {
	pkgs := append([]*pkg.Package(nil), x.b.Packages...)
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Path < pkgs[j].Path })

	methods := map[string]map[string]string{}
	sigs := map[string]map[string]string{
		"Show":  {"show": "func()->str"},
		"Error": {"message": "func()->str", "status": "func()->i32"},
	}
	ifaces := []string{"Show", "Error"}
	derived := map[string]bool{}
	var types []string

	for _, p := range pkgs {
		x.line("package", quote("path", p.Path))
		for _, f := range p.Files {
			x.line("file", quote("path", f.Path), quote("package", p.Path))
			if f.AST == nil {
				continue
			}
			x.emitFile(p, f)
			x.collect(p, f, methods, sigs, derived, &types, &ifaces)
		}
	}
	x.emitSatisfies(methods, sigs, derived, types, ifaces)
	x.emitDispatch(methods, sigs, derived, types)
}

// emitDispatch lists every type that can be the target of an interface call.
func (x *builder) emitDispatch(methods, sigs map[string]map[string]string, derived map[string]bool, types []string) {
	for _, dp := range x.b.DispatchPoints() {
		key := dp.Interface
		required, known := sigs[key]
		if !known {
			if q, ok := x.ifaceKey[key]; ok {
				key = q
				required, known = sigs[key]
			}
		}
		if !known {
			// The interface is outside this load; listing its targets would
			// be a guess, so the line is left out.
			continue
		}
		var targets []string
		for _, t := range types {
			if satisfies(methods[t], required) || derived[t] && key == "Show" {
				targets = append(targets, t)
			}
		}
		sort.Strings(targets)
		x.line("dispatch", quote("file", dp.File), "line="+strconv.Itoa(dp.Pos.Line),
			"col="+strconv.Itoa(dp.Pos.Col), quote("interface", key),
			quote("targets", strings.Join(targets, ",")))
	}
}

func (x *builder) emitFile(p *pkg.Package, f *pkg.File) {
	for _, imp := range f.AST.Imports {
		if imp.C {
			for _, link := range imp.Links {
				x.line("cfunc", quote("header", imp.Path), quote("name", link.Name), quote("linkage", linkage(link)))
			}
			continue
		}
		x.line("import", quote("from", p.Path), quote("to", imp.Path))
	}
	for _, d := range f.AST.Decls {
		switch decl := d.(type) {
		case *ast.ConstDecl:
			x.decl(f.Path, decl.Pos(), "const", decl.Name)
		case *ast.VarDecl:
			x.decl(f.Path, decl.Pos(), "var", decl.Name)
		case *ast.TypeAliasDecl:
			x.decl(f.Path, decl.Pos(), "typealias", decl.Name)
			if decl.Type != nil {
				x.line("alias", quote("file", f.Path), "line="+strconv.Itoa(decl.NamePos.Line),
					"col="+strconv.Itoa(decl.NamePos.Col), quote("name", decl.Name),
					quote("target", typeText(decl.Type)))
			}
		case *ast.FuncDecl:
			x.decl(f.Path, decl.Pos(), "func", decl.Name)
			if decl.Recv != nil {
				pos := decl.Pos()
				x.line("method", quote("file", f.Path), "line="+strconv.Itoa(pos.Line),
					"col="+strconv.Itoa(pos.Col), quote("type", receiver(decl.Recv.Type)),
					quote("name", decl.Name), quote("recv", recvKind(decl.Recv.Type)))
			}
		case *ast.StructDecl:
			x.decl(f.Path, decl.Pos(), "struct", decl.Name)
		case *ast.EnumDecl:
			x.decl(f.Path, decl.Pos(), "enum", decl.Name)
		case *ast.InterfaceDecl:
			x.decl(f.Path, decl.Pos(), "interface", decl.Name)
		case *ast.LaneDecl:
			x.decl(f.Path, decl.Pos(), "lane", decl.Name)
		}
	}
}

func (x *builder) decl(file string, pos source.Pos, kind, name string) {
	x.line("decl", quote("file", file), "line="+strconv.Itoa(pos.Line), "col="+strconv.Itoa(pos.Col),
		quote("kind", kind), quote("name", name), quote("vis", visibility(name)))
}

// collect gathers method sets and interface requirements for satisfies.
func (x *builder) collect(p *pkg.Package, f *pkg.File, methods, sigs map[string]map[string]string, derived map[string]bool, types, ifaces *[]string) {
	for _, d := range f.AST.Decls {
		switch decl := d.(type) {
		case *ast.StructDecl:
			name := qualified(p.Path, decl.Name)
			*types = append(*types, name)
			if methods[name] == nil {
				methods[name] = map[string]string{}
			}
		case *ast.EnumDecl:
			name := qualified(p.Path, decl.Name)
			*types = append(*types, name)
			if methods[name] == nil {
				methods[name] = map[string]string{}
			}
		case *ast.FuncDecl:
			if decl.Recv == nil {
				continue
			}
			name := qualified(p.Path, receiver(decl.Recv.Type))
			if methods[name] == nil {
				methods[name] = map[string]string{}
			}
			methods[name][decl.Name] = signature(decl)
		case *ast.DeriveDecl:
			if decl.Name == "Show" {
				derived[qualified(p.Path, receiver(decl.Type))] = true
			}
		case *ast.InterfaceDecl:
			name := qualified(p.Path, decl.Name)
			*ifaces = append(*ifaces, name)
			x.ifaceKey[decl.Name] = name
			if sigs[name] == nil {
				sigs[name] = map[string]string{}
			}
			for _, m := range decl.Methods {
				sigs[name][m.Name] = sigText(m.Params, m.Result)
			}
		}
	}
}

func (x *builder) emitSatisfies(methods, sigs map[string]map[string]string, derived map[string]bool, types, ifaces []string) {
	sort.Strings(types)
	sort.Strings(ifaces)
	for _, t := range types {
		for _, i := range ifaces {
			if satisfies(methods[t], sigs[i]) || derived[t] && i == "Show" {
				x.line("satisfies", quote("type", t), quote("interface", i))
			}
		}
	}
}

// satisfies reports whether a method set covers every interface requirement.
func satisfies(methods, required map[string]string) bool {
	for name, sig := range required {
		if methods[name] != sig {
			return false
		}
	}
	return true
}

func signature(fn *ast.FuncDecl) string {
	return sigText(fn.Params, fn.Result)
}

func sigText(params []*ast.Param, result *ast.Type) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = typeText(p.Type)
	}
	out := "func(" + strings.Join(parts, ",") + ")"
	if result != nil {
		out += "->" + typeText(result)
	}
	return out
}

func typeText(t *ast.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case ast.ScalarType, ast.NamedType:
		if len(t.Args) == 0 {
			return t.Name
		}
		parts := make([]string, len(t.Args))
		for i, a := range t.Args {
			parts[i] = typeText(a)
		}
		return t.Name + "<" + strings.Join(parts, ",") + ">"
	case ast.ArrayType:
		return "[" + t.Len + "]" + typeText(t.Elem)
	case ast.ViewType:
		return "[]" + typeText(t.Elem)
	case ast.FuncType:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = typeText(p)
		}
		out := "func(" + strings.Join(parts, ",") + ")"
		if t.Result != nil {
			out += "->" + typeText(t.Result)
		}
		return out
	case ast.UnitType:
		return "()"
	}
	return ""
}

func receiver(t *ast.Type) string {
	if t == nil {
		return ""
	}
	return t.Name
}

func recvKind(t *ast.Type) string {
	if t == nil {
		return "value"
	}
	if t.IsRef() {
		return "ref"
	}
	if t.IsMutRef() || t.ViewMut {
		return "mut"
	}
	return "value"
}

func linkage(link *ast.Link) string {
	switch {
	case link.Fast:
		return "fast"
	case link.Blocking:
		return "blocking"
	}
	return "plain"
}

func visibility(name string) string {
	if name == "" || strings.HasPrefix(name, "__") {
		return "private"
	}
	return "public"
}

func qualified(pkgPath, name string) string {
	if pkgPath == "" {
		return name
	}
	return pkgPath + "." + name
}

func quote(name, value string) string {
	return name + "=" + willc.Quote(value)
}
