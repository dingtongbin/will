package resolve

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// Exported is one package's exported top-level names.
type Exported map[string]*Symbol

// Imports maps an import path to the exported names of that package.
type Imports map[string]Exported

// Resolve binds every name in f and returns the diagnostics it found.
func Resolve(f *ast.File) (*Result, []diag.Diagnostic) {
	return ResolveWith(f, nil)
}

// ResolveWith binds every name in f. Imports supplies the exported names of
// the imported packages, so package-qualified members resolve for real.
func ResolveWith(f *ast.File, imports Imports) (*Result, []diag.Diagnostic) {
	return ResolveIn(f, nil, imports)
}

// ResolveIn also sees the top-level names of the other files of f's own
// package, so one package resolves as a unit: a name declared in a sibling
// file is found, and a name declared twice in the package is a duplicate.
func ResolveIn(f *ast.File, packageNames Exported, imports Imports) (*Result, []diag.Diagnostic) {
	r := &resolver{
		file:              f,
		pkg:               newScope(nil),
		refs:              map[ast.Node]*Symbol{},
		methods:           map[string]*Symbol{},
		packages:          imports,
		tables:            map[string]Exported{},
		derives:           map[string]bool{},
		unknownQualifiers: map[string]bool{},
	}
	r.fileScope = newScope(r.pkg)
	r.addPredeclared()
	r.addPackageNames(packageNames)
	r.addImports()
	r.addTopLevel()
	r.walkFile()
	r.reportUnusedImports()
	res := &Result{File: f, Package: r.pkg, Refs: r.refs, Symbols: r.symbols}
	return res, r.bag.Diagnostics()
}

type resolver struct {
	file      *ast.File
	pkg       *scope
	fileScope *scope
	stack     []*scope
	bag       diag.Bag
	refs      map[ast.Node]*Symbol
	symbols   []*Symbol
	imports   []*importRec
	methods   map[string]*Symbol
	packages  Imports
	tables    map[string]Exported
	derives   map[string]bool
	cImport   *Symbol
	// unknownQualifiers holds the package names whose import path matched no
	// package, so their members are not checked.
	unknownQualifiers map[string]bool
}

type importRec struct {
	sym        *Symbol
	pos        source.Pos
	sideEffect bool
	c          bool
	unknown    bool
	links      []*Symbol
}

var predeclaredTypes = []string{
	"bool", "str", "raw",
	"i8", "i16", "i32", "i64", "isize",
	"u8", "u16", "u32", "u64", "usize",
	"f32", "f64",
	"Array", "Map", "Set",
	"chan", "rc", "weak", "Task",
	"Option", "Result", "Value", "Duration",
	"Show", "Error", "TaskError",
	// Equal and Ord are built-in constraints: they name a property the
	// compiler implements for each type, and they are only usable as type
	// parameter constraints.
	"equal", "Ord",
}

// isPredeclaredType reports whether a name is one of the type names every
// package starts with.
func isPredeclaredType(name string) bool {
	for _, known := range predeclaredTypes {
		if known == name {
			return true
		}
	}
	return false
}

var predeclaredValues = []string{
	"Some", "None", "Ok", "Err", "wait",
	"Canceled", "Deadline",
	"print", "panic", "assert", "leak",
	"take_c", "take_c_rc",
	"SizeOf", "AlignOf", "OffsetOf",
	"Cancel", "Priority",
}

func (r *resolver) addPredeclared() {
	for _, name := range predeclaredTypes {
		r.pkg.define(&Symbol{Name: name, Kind: PredeclaredTypeKind, Exported: true, Predeclared: true})
	}
	for _, name := range predeclaredValues {
		r.pkg.define(&Symbol{Name: name, Kind: PredeclaredValueKind, Exported: true, Predeclared: true})
	}
}

// addPackageNames seeds the package scope with the top-level names declared in
// the other files of this package. A collision with one of them is a duplicate
// name, reported once by the package layer, so this file's declaration quietly
// replaces the seeded one.
func (r *resolver) addPackageNames(names Exported) {
	for _, sym := range names {
		if sym == nil || sym.Name == "" {
			continue
		}
		seeded := &Symbol{
			Name:        sym.Name,
			Kind:        sym.Kind,
			Pos:         sym.Pos,
			Node:        sym.Node,
			RecvType:    sym.RecvType,
			Exported:    sym.Exported,
			FromSibling: true,
		}
		if seeded.Kind == VariantKind {
			r.pkg.defineVariant(seeded)
			continue
		}
		r.pkg.define(seeded)
	}
}

func (r *resolver) addImports() {
	seen := map[string]bool{}
	for _, imp := range r.file.Imports {
		if seen[imp.Path] {
			r.bag.Error(imp.Span.Start, diag.CodeDuplicateImport, "duplicate import", "remove the earlier one")
			continue
		}
		seen[imp.Path] = true
		rec := &importRec{pos: imp.Span.Start, sideEffect: imp.SideEffect, c: imp.C}
		switch {
		case imp.SideEffect:
			if _, known := r.packages[imp.Path]; !known {
				rec.unknown = true
				r.unknownImport(imp)
			}
		case imp.C:
			// C names are reached through the c qualifier, so the import
			// block binds one name, c, whatever headers it names.
			if r.cImport == nil {
				sym := &Symbol{Name: "c", Kind: ImportKind, Pos: imp.Span.Start, Node: imp}
				if prev := r.fileScope.names["c"]; prev != nil {
					r.duplicate(sym.Pos, "c")
				} else {
					r.fileScope.define(sym)
					r.symbols = append(r.symbols, sym)
				}
				r.cImport = sym
				r.tables["c"] = Exported{}
				rec.sym = sym
			}
			table := r.tables["c"]
			for _, link := range imp.Links {
				sym := &Symbol{
					Name:     link.Name,
					Kind:     CLinkKind,
					Pos:      link.Span.Start,
					Node:     link,
					Exported: true,
				}
				r.symbols = append(r.symbols, sym)
				if table != nil {
					table[link.Name] = sym
				}
				rec.links = append(rec.links, sym)
			}
		default:
			name := imp.Alias
			if name == "" {
				name = packageName(imp.Path)
			}
			if name == "" {
				break
			}
			table, known := r.packages[imp.Path]
			sym := &Symbol{Name: name, Kind: ImportKind, Pos: imp.Span.Start, Node: imp}
			if prev := r.fileScope.names[name]; prev != nil {
				r.duplicate(sym.Pos, name)
			} else {
				r.fileScope.define(sym)
				r.symbols = append(r.symbols, sym)
			}
			rec.sym = sym
			if known {
				r.tables[name] = table
			} else {
				// The path matches no package: report the root cause and
				// suppress every member diagnostic inside this import, which
				// cannot be judged without the package.
				rec.unknown = true
				r.unknownQualifiers[name] = true
				r.unknownImport(imp)
			}
		}
		r.imports = append(r.imports, rec)
	}
}

// unknownImport reports B0008 for an import path that matches no package. The
// help names the closest known path when there is one.
func (r *resolver) unknownImport(imp *ast.Import) {
	help := "the path must be a module path plus directories; run will get if the dependency is not fetched yet"
	if closest := closestName(imp.Path, r.packagePaths()); closest != "" {
		help = "did you mean " + closest + "?"
	}
	r.bag.Error(imp.Span.Start, diag.CodeUnknownImportPath, "unknown import path "+imp.Path, help)
}

// packagePaths lists the import paths this run knows, for the B0008 help.
func (r *resolver) packagePaths() []string {
	out := make([]string, 0, len(r.packages))
	for path := range r.packages {
		out = append(out, path)
	}
	return out
}

func packageName(path string) string {
	path = strings.TrimSuffix(path, "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func (r *resolver) addTopLevel() {
	for _, d := range r.file.Decls {
		switch decl := d.(type) {
		case *ast.ConstDecl:
			r.defineTop(&Symbol{Name: decl.Name, Kind: ConstKind, Pos: decl.NamePos, Node: decl, Exported: isExported(decl.Name)})
		case *ast.VarDecl:
			r.defineTop(&Symbol{Name: decl.Name, Kind: VarKind, Pos: decl.NamePos, Node: decl, Exported: isExported(decl.Name)})
		case *ast.TypeAliasDecl:
			r.defineTop(&Symbol{Name: decl.Name, Kind: TypeAliasKind, Pos: decl.NamePos, Node: decl, Exported: isExported(decl.Name)})
		case *ast.FuncDecl:
			if decl.Recv != nil {
				r.addMethod(decl)
				continue
			}
			r.defineTop(&Symbol{Name: decl.Name, Kind: FuncKind, Pos: decl.NamePos, Node: decl, Exported: isExported(decl.Name)})
		case *ast.StructDecl:
			r.defineTop(&Symbol{Name: decl.Name, Kind: StructKind, Pos: decl.NamePos, Node: decl, Exported: isExported(decl.Name)})
		case *ast.EnumDecl:
			r.defineTop(&Symbol{Name: decl.Name, Kind: EnumKind, Pos: decl.NamePos, Node: decl, Exported: isExported(decl.Name)})
			for _, v := range decl.Variants {
				// A variant is selected through its enum type, so it lives in
				// the variant table rather than in the package name table.
				sym := &Symbol{Name: v.Name, Kind: VariantKind, Pos: v.NamePos, Node: v, Exported: isExported(v.Name)}
				if prev, ok := r.pkg.variants[v.Name]; ok {
					if prev.FromSibling {
						// The package layer already reported a duplicate
						// between files; this file's declaration is the one
						// its own patterns should see.
						r.pkg.variants[v.Name] = sym
						continue
					}
					r.duplicate(v.NamePos, prev.Name)
					continue
				}
				r.pkg.defineVariant(sym)
			}
		case *ast.InterfaceDecl:
			r.defineTop(&Symbol{Name: decl.Name, Kind: InterfaceKind, Pos: decl.NamePos, Node: decl, Exported: isExported(decl.Name)})
		case *ast.LaneDecl:
			r.defineTop(&Symbol{Name: decl.Name, Kind: LaneKind, Pos: decl.NamePos, Node: decl, Exported: isExported(decl.Name)})
			switch inner := decl.Decl.(type) {
			case *ast.StructDecl:
				r.defineTop(&Symbol{Name: inner.Name, Kind: StructKind, Pos: inner.NamePos, Node: inner, Exported: isExported(inner.Name)})
			case *ast.EnumDecl:
				r.defineTop(&Symbol{Name: inner.Name, Kind: EnumKind, Pos: inner.NamePos, Node: inner, Exported: isExported(inner.Name)})
			case *ast.InterfaceDecl:
				r.defineTop(&Symbol{Name: inner.Name, Kind: InterfaceKind, Pos: inner.NamePos, Node: inner, Exported: isExported(inner.Name)})
			}
		}
	}
}

func (r *resolver) addMethod(fn *ast.FuncDecl) {
	key := typeName(fn.Recv.Type) + "." + fn.Name
	sym := &Symbol{Name: fn.Name, Kind: MethodKind, Pos: fn.NamePos, Node: fn, RecvType: typeName(fn.Recv.Type), Exported: isExported(fn.Name)}
	if _, ok := r.methods[key]; ok {
		r.duplicate(fn.NamePos, fn.Name)
		return
	}
	r.methods[key] = sym
	r.symbols = append(r.symbols, sym)
}

// defineTop adds one package-level symbol. Shadowing a predeclared name is a
// warning; two declarations with the same name are an error.
func (r *resolver) defineTop(sym *Symbol) {
	if sym.Name == "" {
		return
	}
	if sym.Name == "_" {
		r.bag.Error(sym.Pos, diag.CodeTopLevelDiscard,
			`"_" is not a name; it is the discard binding`, "give the declaration a name")
		return
	}
	prev := r.pkg.define(sym)
	if prev == nil {
		r.symbols = append(r.symbols, sym)
		return
	}
	if prev.FromSibling {
		// A name declared in this file and in a sibling file is a duplicate;
		// the package layer reports it once.
		r.symbols = append(r.symbols, sym)
		return
	}
	if prev.Predeclared {
		r.shadow(sym.Pos, sym.Name)
		r.symbols = append(r.symbols, sym)
		return
	}
	r.pkg.names[sym.Name] = prev
	r.duplicate(sym.Pos, sym.Name)
}

func (r *resolver) walkFile() {
	for _, d := range r.file.Decls {
		r.walkDecl(d)
	}
}

func (r *resolver) walkDecl(d ast.Decl) {
	switch decl := d.(type) {
	case *ast.ConstDecl:
		r.walkType(decl.Type)
		r.walkExpr(decl.Value, true)
	case *ast.VarDecl:
		r.walkType(decl.Type)
		r.walkExpr(decl.Value, true)
	case *ast.TypeAliasDecl:
		r.walkType(decl.Type)
	case *ast.FuncDecl:
		r.walkFunc(decl)
	case *ast.StructDecl:
		r.push()
		r.declareTypeParams(decl.TypeParams)
		for _, f := range decl.Fields {
			r.walkType(f.Type)
			if f.Default != nil {
				r.walkExpr(f.Default, true)
			}
		}
		r.pop()
	case *ast.EnumDecl:
		r.push()
		r.declareTypeParams(decl.TypeParams)
		for _, v := range decl.Variants {
			for _, t := range v.Args {
				r.walkType(t)
			}
		}
		r.pop()
	case *ast.InterfaceDecl:
		r.push()
		r.declareTypeParams(decl.TypeParams)
		for _, m := range decl.Methods {
			r.push()
			for _, pm := range m.Params {
				r.declareLocal(&Symbol{Name: pm.Name, Kind: ParamKind, Pos: pm.NamePos, Node: pm, Exported: isExported(pm.Name)})
				r.walkType(pm.Type)
			}
			r.walkType(m.Result)
			r.pop()
		}
		r.pop()
	case *ast.DeriveDecl:
		switch decl.Name {
		case "Json", "Show":
			key := decl.Name + " " + typeName(decl.Type)
			if r.derives[key] {
				r.bag.Error(decl.Span.Start, diag.CodeDuplicateDerive,
					"duplicate derive for "+typeName(decl.Type), "remove the earlier one")
			}
			r.derives[key] = true
		default:
			// The generator list is fixed at Json and Show. Equal and Ord are
			// predeclared constraints, so saying "undefined name" would be
			// wrong.
			r.bag.Error(decl.NamePos, diag.CodeBadDeriveName,
				"cannot derive "+decl.Name+" for "+typeName(decl.Type), "derive takes Json or Show")
		}
		r.walkType(decl.Type)
		if sym, ok := r.refs[decl.Type]; ok {
			r.refs[decl] = sym
		}
	case *ast.LaneDecl:
		if decl.Decl != nil {
			r.refs[decl] = r.pkg.names[decl.Name]
			r.walkDecl(decl.Decl)
		}
	}
}

func (r *resolver) walkFunc(decl *ast.FuncDecl) {
	r.push()
	if decl.Recv != nil {
		r.declareReceiverTypeParams(decl.Recv.Type)
		r.walkType(decl.Recv.Type)
		r.declareLocal(&Symbol{Name: decl.Recv.Name, Kind: ReceiverKind, Pos: decl.Recv.NamePos, Node: decl.Recv, Exported: isExported(decl.Recv.Name)})
	}
	r.declareTypeParams(decl.TypeParams)
	for _, pm := range decl.Params {
		r.declareLocal(&Symbol{Name: pm.Name, Kind: ParamKind, Pos: pm.NamePos, Node: pm, Exported: isExported(pm.Name)})
		r.walkType(pm.Type)
	}
	r.walkType(decl.Result)
	r.walkBlock(decl.Body)
	r.pop()
}

// declareReceiverTypeParams brings the type parameters written in a generic
// receiver into scope, so a method on Box<T> may name T in its signature and
// body without redeclaring it.
func (r *resolver) declareReceiverTypeParams(t *ast.Type) {
	if t == nil {
		return
	}
	for _, a := range t.Args {
		if a == nil || a.Kind != ast.NamedType || len(a.Args) != 0 {
			continue
		}
		r.declareLocal(&Symbol{Name: a.Name, Kind: TypeParamKind, Pos: a.Pos(), Node: a, Exported: isExported(a.Name)})
	}
}

func (r *resolver) declareTypeParams(params []*ast.TypeParam) {
	for _, tp := range params {
		r.declareLocal(&Symbol{Name: tp.Name, Kind: TypeParamKind, Pos: tp.NamePos, Node: tp, Exported: isExported(tp.Name)})
		for _, c := range tp.Constraints {
			r.walkType(c)
		}
	}
}

func (r *resolver) push() {
	r.stack = append(r.stack, newScope(r.current()))
}

func (r *resolver) pop() {
	top := r.current()
	for _, sym := range top.order {
		r.reportUnusedLocal(sym)
	}
	r.stack = r.stack[:len(r.stack)-1]
}

func (r *resolver) current() *scope {
	if len(r.stack) == 0 {
		return r.fileScope
	}
	return r.stack[len(r.stack)-1]
}

// declareLocal adds a binding to the current scope and warns when it shadows
// an outer name.
func (r *resolver) declareLocal(sym *Symbol) {
	if sym.Name == "" || sym.Name == "_" {
		return
	}
	// Visibility is a property of package-level declarations. A local binding
	// never becomes exported merely because it has no __ prefix.
	sym.Exported = false
	scope := r.current()
	if _, ok := scope.lookup(sym.Name); ok {
		r.shadow(sym.Pos, sym.Name)
	}
	prev := scope.define(sym)
	if prev == nil {
		r.symbols = append(r.symbols, sym)
		return
	}
	if !prev.Predeclared {
		scope.names[sym.Name] = prev
		return
	}
	r.symbols = append(r.symbols, sym)
}

func (r *resolver) reportUnusedLocal(sym *Symbol) {
	switch sym.Kind {
	case ParamKind, ReceiverKind, TypeParamKind, PredeclaredTypeKind, PredeclaredValueKind:
		return
	}
	if sym.Used || sym.Name == "_" || sym.Exported {
		return
	}
	r.bag.Error(sym.Pos, diag.CodeUnusedVar, "variable "+sym.Name+" is never read", "delete it, read it, or discard it with _")
}

func (r *resolver) reportUnusedImports() {
	for _, rec := range r.imports {
		if rec.sideEffect {
			continue
		}
		if rec.c {
			used := false
			for _, link := range rec.links {
				if link.Used {
					used = true
				}
			}
			if !used {
				r.bag.Error(rec.pos, diag.CodeUnusedImport, "unused import", "remove it, or write import _ for side effects only")
			}
			continue
		}
		if rec.sym != nil && !rec.sym.Used {
			r.bag.Error(rec.pos, diag.CodeUnusedImport, "unused import", "remove it, or write import _ for side effects only")
		}
	}
}

func (r *resolver) undefined(pos source.Pos, name string) {
	r.undefinedIn(pos, name, r.visibleNames())
}

func (r *resolver) undefinedIn(pos source.Pos, name string, candidates []string) {
	help := "declare it first"
	if s := closestName(name, candidates); s != "" {
		help = "did you mean " + s + "?"
	}
	r.bag.Error(pos, diag.CodeUndefinedName, "undefined name", help)
}

func (r *resolver) visibleNames() []string {
	var out []string
	for i := len(r.stack) - 1; i >= 0; i-- {
		for _, sym := range r.stack[i].order {
			out = append(out, sym.Name)
		}
	}
	for _, sym := range r.pkg.order {
		out = append(out, sym.Name)
	}
	for _, sym := range r.fileScope.order {
		out = append(out, sym.Name)
	}
	return out
}

func (r *resolver) shadow(pos source.Pos, name string) {
	r.bag.Warning(pos, diag.CodeShadow, "this name shadows "+name, "rename one of them")
}

func (r *resolver) duplicate(pos source.Pos, name string) {
	r.bag.Error(pos, diag.CodeDuplicateName, "duplicate top-level name "+name,
		"names must be unique in a package: shadowing is a warning, two declarations with the same name are an error")
}

func isExported(name string) bool {
	return name != "" && !strings.HasPrefix(name, "__")
}

// typeName returns the simple name of a named type, or "" for any other shape.
func typeName(t *ast.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case ast.NamedType, ast.ScalarType:
		return t.Name
	}
	return ""
}
