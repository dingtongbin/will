package index

import "strings"

// Package is one package line.
type Package struct {
	Path string
}

// File is one source file line.
type File struct {
	Path    string
	Package string
}

// Import is one package dependency edge.
type Import struct {
	From string
	To   string
}

// Decl is one top-level declaration. Decls holds the decl lines in text
// order. DeclAt also reports a method as a Decl whose Kind is "method" and
// whose Type and Recv are the receiver; Pkg is derived from the file line.
type Decl struct {
	File string
	Pkg  string
	Line int
	Col  int
	Kind string
	Name string
	Vis  string
	Type string
}

// Alias is one type alias declaration. Target is the canonical target type
// text; the alias has no type identity of its own.
type Alias struct {
	File   string
	Line   int
	Col    int
	Name   string
	Target string
}

// Method is one method declaration. Pkg is derived from the file line.
type Method struct {
	File string
	Pkg  string
	Line int
	Col  int
	Type string
	Name string
}

// Satisfies is one structural satisfaction fact.
type Satisfies struct {
	Type      string
	Interface string
}

// Dispatch is one dynamic dispatch call site. Targets is empty when the
// interface is known to have no implementation in this load.
type Dispatch struct {
	File      string
	Line      int
	Col       int
	Interface string
	Targets   []string
}

// Target is one pair of a dispatch site and a possible callee type, expanded
// from the targets field. A dispatch line with an empty targets field
// contributes no Target.
type Target struct {
	File string
	Line int
	Col  int
	Type string
}

// CFunc is one C symbol the module imports.
type CFunc struct {
	Header  string
	Name    string
	Linkage string
}

// Index is the parsed form of the text index. The text is the single source
// of truth: every exported slice holds one line in text order, and Write
// reproduces the bytes Parse read. Query answers are derived from those
// lines at parse time, so if a caller edits a slice the text still wins. A
// parsed Index is read-only.
type Index struct {
	Packages   []Package
	Files      []File
	Imports    []Import
	Decls      []Decl
	Aliases    []Alias
	Methods    []Method
	Satisfies  []Satisfies
	Dispatches []Dispatch
	CFuncs     []CFunc
	Targets    []Target

	// lines holds one rebuilt line per parsed record. Write joins them, so it
	// never has to normalize a field the parser accepted.
	lines []string

	packageOf       map[string]string
	importsOf       map[string][]string
	declByName      map[declKey]int
	methodOf        map[methodKey]int
	declAt          map[posKey]int
	methodAt        map[posKey]int
	methodDecls     []Decl
	dispatchAt      map[DispatchKey]int
	interfacesOf    map[string][]string
	implementations map[string][]string
}

// newIndex returns an index with the query maps ready for Parse.
func newIndex() *Index {
	return &Index{
		packageOf:       map[string]string{},
		importsOf:       map[string][]string{},
		declByName:      map[declKey]int{},
		methodOf:        map[methodKey]int{},
		declAt:          map[posKey]int{},
		methodAt:        map[posKey]int{},
		dispatchAt:      map[DispatchKey]int{},
		interfacesOf:    map[string][]string{},
		implementations: map[string][]string{},
	}
}

// Write renders the index as text. Parse accepts only the one shape the
// format allows, so the result is byte-identical to the text Parse read.
func (ix *Index) Write() []byte {
	var b strings.Builder
	for _, line := range ix.lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
