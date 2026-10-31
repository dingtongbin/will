package index

import "strconv"

// DispatchKey identifies one dispatch line by its source position. A dispatch
// line carries no id and no call spelling, so its position is the only stable
// key the text can express. The parser rejects two dispatch lines at the same
// position because a caller holding one position could not tell them apart.
type DispatchKey struct {
	File string
	Line int
	Col  int
}

// String formats the key the way an error message names it.
func (k DispatchKey) String() string {
	return k.File + ":" + strconv.Itoa(k.Line) + ":" + strconv.Itoa(k.Col)
}

type declKey struct {
	pkg  string
	name string
}

type methodKey struct {
	pkg  string
	typ  string
	name string
}

type posKey struct {
	file string
	line int
	col  int
}

// buildLookups derives the package of every decl, method, and file, then
// fills the query maps. First position wins where the text repeats a key:
// the builder writes both a decl line and a method line for a method, so a
// position can legitimately name both.
func (ix *Index) buildLookups() {
	for _, f := range ix.Files {
		if _, ok := ix.packageOf[f.Path]; !ok {
			ix.packageOf[f.Path] = f.Package
		}
	}
	for i := range ix.Decls {
		d := &ix.Decls[i]
		d.Pkg = ix.packageOf[d.File]
		key := declKey{pkg: d.Pkg, name: d.Name}
		if _, ok := ix.declByName[key]; !ok {
			ix.declByName[key] = i
		}
		pos := posKey{file: d.File, line: d.Line, col: d.Col}
		if _, ok := ix.declAt[pos]; !ok {
			ix.declAt[pos] = i
		}
	}
	for i := range ix.Methods {
		m := &ix.Methods[i]
		m.Pkg = ix.packageOf[m.File]
		key := methodKey{pkg: m.Pkg, typ: m.Type, name: m.Name}
		if _, ok := ix.methodOf[key]; !ok {
			ix.methodOf[key] = i
		}
		pos := posKey{file: m.File, line: m.Line, col: m.Col}
		if _, ok := ix.methodAt[pos]; !ok {
			ix.methodAt[pos] = i
		}
	}
	ix.methodDecls = make([]Decl, len(ix.Methods))
	for i, m := range ix.Methods {
		ix.methodDecls[i] = Decl{
			File: m.File,
			Pkg:  m.Pkg,
			Line: m.Line,
			Col:  m.Col,
			Kind: "method",
			Name: m.Name,
			Vis:  visibility(m.Name),
			Type: m.Type,
		}
	}

	// The builder writes one import line per importing file, so the same
	// package pair can repeat; one edge is the fact.
	edges := map[string]map[string]bool{}
	for _, im := range ix.Imports {
		seen := edges[im.From]
		if seen == nil {
			seen = map[string]bool{}
			edges[im.From] = seen
		}
		if seen[im.To] {
			continue
		}
		seen[im.To] = true
		ix.importsOf[im.From] = append(ix.importsOf[im.From], im.To)
	}

	pairs := map[Satisfies]bool{}
	for _, s := range ix.Satisfies {
		if pairs[s] {
			continue
		}
		pairs[s] = true
		ix.interfacesOf[s.Type] = append(ix.interfacesOf[s.Type], s.Interface)
		ix.implementations[s.Interface] = append(ix.implementations[s.Interface], s.Type)
	}
}

// PackageOf returns the package that contains file.
func (ix *Index) PackageOf(file string) (string, bool) {
	pkg, ok := ix.packageOf[file]
	return pkg, ok
}

// DeclAt returns the declaration that starts at the 1-based line and column.
// A method line wins over the decl line the builder writes at the same
// position, because the method line also gives the receiver.
func (ix *Index) DeclAt(file string, line, col int) (*Decl, bool) {
	pos := posKey{file: file, line: line, col: col}
	if i, ok := ix.methodAt[pos]; ok {
		return &ix.methodDecls[i], true
	}
	if i, ok := ix.declAt[pos]; ok {
		return &ix.Decls[i], true
	}
	return nil, false
}

// DeclByName returns the first top-level declaration named name in pkg. The
// format does not make bare names unique: the builder writes a decl line for
// a method under its bare name, so two types of one package can repeat one.
func (ix *Index) DeclByName(pkg, name string) (*Decl, bool) {
	i, ok := ix.declByName[declKey{pkg: pkg, name: name}]
	if !ok {
		return nil, false
	}
	return &ix.Decls[i], true
}

// MethodOf returns the method named method on typeName in pkg.
func (ix *Index) MethodOf(pkg, typeName, method string) (*Method, bool) {
	i, ok := ix.methodOf[methodKey{pkg: pkg, typ: typeName, name: method}]
	if !ok {
		return nil, false
	}
	return &ix.Methods[i], true
}

// ImportsOf returns the package paths that pkg imports, in first-appearance
// order and without repeats. It returns nil for a package with no import
// lines and for a package the index does not name.
func (ix *Index) ImportsOf(pkg string) []string {
	paths := ix.importsOf[pkg]
	if len(paths) == 0 {
		return nil
	}
	return append([]string(nil), paths...)
}

// DispatchAt returns the dispatch record at key.
func (ix *Index) DispatchAt(key DispatchKey) (*Dispatch, bool) {
	i, ok := ix.dispatchAt[key]
	if !ok {
		return nil, false
	}
	return &ix.Dispatches[i], true
}

// DispatchTargets returns the callee type names of the dispatch at key. An
// empty result with ok true means the interface has no implementation in
// this load; ok false means the index has no such dispatch line.
func (ix *Index) DispatchTargets(key DispatchKey) ([]string, bool) {
	i, ok := ix.dispatchAt[key]
	if !ok {
		return nil, false
	}
	return append([]string(nil), ix.Dispatches[i].Targets...), true
}

// InterfacesOf returns the interfaces that typeName satisfies, in the order
// the satisfies lines appear and without repeats.
func (ix *Index) InterfacesOf(typeName string) []string {
	names := ix.interfacesOf[typeName]
	if len(names) == 0 {
		return nil
	}
	return append([]string(nil), names...)
}

// Implementations returns the types that satisfy iface, in the order the
// satisfies lines appear and without repeats.
func (ix *Index) Implementations(iface string) []string {
	names := ix.implementations[iface]
	if len(names) == 0 {
		return nil
	}
	return append([]string(nil), names...)
}
