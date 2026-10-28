package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// globalInfo is one top-level variable or constant as the lowering sees it.
type globalInfo struct {
	sym  string
	typ  string
	text string
	// isConst marks a constant, which has no storage; value is its literal
	// spelling when the initializer is a literal.
	isConst bool
	value   string
}

// collectGlobals registers every top-level variable of the module before any
// function is emitted, so a function may name a global of another document.
func (g *generator) collectGlobals() {
	g.globalSymbols = map[string]*globalInfo{}
	g.inits = nil
	for _, d := range g.docs {
		g.use(d)
		for _, r := range g.topLevelVars(d) {
			text := fieldText(r, "type")
			// A named type declared in the same package needs its package path
			// in the text another document reads, or the type cannot be found.
			if q := qualify(d.path, text); g.declKinds[q] != "" {
				text = q
			}
			info := &globalInfo{sym: qualify(d.path, fieldText(r, "text")), text: text}
			typ, err := g.declLLVMType(r.ID)
			if err != nil {
				// An unsupported global type is reported when the variable is
				// used; leaving it out keeps the error at the use site.
				continue
			}
			info.typ = typ
			if d.globals == nil {
				d.globals = map[int]*globalInfo{}
			}
			d.globals[r.ID] = info
			g.globalSymbols[info.sym] = info
			if g.isClassText(text) {
				g.needGCDecl = true
				g.gcGlobalRoots = append(g.gcGlobalRoots, info.sym)
			} else if _, ok := g.ifaceOfText(text); ok {
				g.needGCDecl = true
				g.gcGlobalRoots = append(g.gcGlobalRoots, info.sym)
			}
		}
		for _, r := range d.recs {
			if r.Kind != "const" || refField(r, "parent") != d.moduleID {
				continue
			}
			info := &globalInfo{sym: qualify(d.path, fieldText(r, "text")), text: fieldText(r, "type"), isConst: true}
			if typ, err := g.declLLVMType(r.ID); err == nil {
				info.typ = typ
			}
			if value, ok := g.constValueOf(r.ID); ok {
				info.value = value
			}
			g.globalSymbols[info.sym] = info
		}
	}
}

// topLevelVars lists the var records of one document in source order. A var is
// top-level when its parent is the module record.
func (g *generator) topLevelVars(d *docState) []willc.Record {
	var out []willc.Record
	for _, r := range d.recs {
		if r.Kind != "var" || refField(r, "parent") != d.moduleID {
			continue
		}
		out = append(out, r)
	}
	sortByID(out)
	return out
}

// emitGlobals writes the storage of one document's top-level variables.
func (g *generator) emitGlobals(d *docState) {
	for _, r := range g.topLevelVars(d) {
		info := d.globals[r.ID]
		if info == nil {
			continue
		}
		prefix := "internal"
		if g.packageMode {
			prefix = "hidden"
		}
		fmt.Fprintf(g.out, "@%s = %s global %s zeroinitializer\n", llvmSymbol(info.sym), prefix, info.typ)
	}
}

// emitInit writes one package's initialization function. It stores each
// initialized variable in declaration order; a variable without an initializer
// keeps the zero value the global already holds.
func (g *generator) emitInit(d *docState) error {
	var vars []willc.Record
	for _, r := range g.topLevelVars(d) {
		if d.globals[r.ID] == nil {
			continue
		}
		if len(g.children(r.ID)) != 0 {
			vars = append(vars, r)
		}
	}
	if len(vars) == 0 {
		return nil
	}
	sym := qualify(d.path, "will$init")
	g.inits = append(g.inits, sym)
	g.fnSymbol = sym
	g.fnResultText = ""
	g.labels = 0
	g.slots = map[int]string{}
	g.slotTypes = map[int]string{}
	g.slotNames = map[string]int{}
	g.loops = nil
	g.scope = map[int]string{}
	fmt.Fprintf(g.out, "define void @%s() {\nentry:\n", llvmSymbol(sym))
	for _, r := range vars {
		info := d.globals[r.ID]
		child := g.children(r.ID)[0].ID
		ptr := "@" + llvmSymbol(info.sym)
		if iface, ok := g.ifaceOfType(info.typ); ok {
			sourceText, err := g.willTypeText(child)
			if err != nil {
				return err
			}
			if _, err := g.ifaceStoreValue(ptr, child, iface, sourceText); err != nil {
				return err
			}
			if g.gcProgram != nil {
				dataPtr, _ := g.ifaceWords(ptr, iface)
				data := g.load("ptr", dataPtr)
				g.needGCDecl = true
				fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
					ptr, data)
			}
			continue
		}
		if isAggregateType(info.typ) {
			src, srcType, err := g.place(child)
			if err != nil {
				return err
			}
			if srcType != info.typ {
				return g.errf(r.ID, "cannot translate node #%d: the initializer needs %s", r.ID, info.typ)
			}
			g.copyValue(src, ptr, info.typ)
			continue
		}
		value, err := g.value(child)
		if err != nil {
			return err
		}
		if g.isClassText(info.text) {
			g.needGCDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
				ptr, value)
			continue
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", info.typ, value, ptr)
	}
	g.out.WriteString("  ret void\n}\n")
	return nil
}

// canonicalTypeText replaces the package name a document writes with the
// import path the module uses, so a type of another package has one key in the
// module-wide indexes.
func (g *generator) canonicalTypeText(text string) string {
	if dot := strings.IndexByte(text, '.'); dot > 0 {
		if path := g.pkgNames[text[:dot]]; path != "" {
			return path + text[dot:]
		}
	}
	return text
}

// globalByName returns the top-level variable a qualified name selects.
func (g *generator) globalByName(name string) *globalInfo {
	return g.globalSymbols[name]
}

// constValueOf returns the scalar text of a constant declared in the current
// document, so a use of it can be substituted.
func (g *generator) constValueOf(id int) (string, bool) {
	r, ok := g.recs[id]
	if !ok || r.Kind != "const" {
		return "", false
	}
	kids := g.children(id)
	if len(kids) != 1 {
		return "", false
	}
	lit, ok := g.recs[kids[0].ID]
	if !ok {
		return "", false
	}
	if lit.Kind == "un" && fieldText(lit, "op") == "-" {
		inner, ok := g.recs[refField(lit, "expr")]
		if !ok || inner.Kind != "lit" {
			return "", false
		}
		switch fieldText(inner, "kind") {
		case "int", "char":
			return "-" + literalIntText(fieldText(inner, "text")), true
		case "float":
			return "-" + literalFloatText(fieldText(inner, "text")), true
		}
		return "", false
	}
	if lit.Kind != "lit" {
		return "", false
	}
	switch fieldText(lit, "kind") {
	case "int", "char":
		return literalIntText(fieldText(lit, "text")), true
	case "float":
		return literalFloatText(fieldText(lit, "text")), true
	case "bool":
		return literalBoolText(fieldText(lit, "text")), true
	}
	return "", false
}

// globalRef returns the info of a global name, checking the document-local
// reference first and then the qualified name written in the record.
func (g *generator) globalRef(r willc.Record) *globalInfo {
	if info := g.cur.globals[refField(r, "to")]; info != nil {
		g.noteExternalGlobal(info)
		return info
	}
	if name := textField(r, "to"); name != "" {
		info := g.globalByName(name)
		g.noteExternalGlobal(info)
		return info
	}
	return nil
}

// importMember returns the top-level variable or constant a package-qualified
// field names, such as lib.Seed. The receiver must be an import qualifier, so
// an ordinary struct field never takes this path.
func (g *generator) importMember(r willc.Record) (*globalInfo, bool) {
	recv, ok := g.recs[refField(r, "recv")]
	if !ok || recv.Kind != "name" {
		return nil, false
	}
	imp, ok := g.recs[refField(recv, "to")]
	if !ok || imp.Kind != "import" {
		return nil, false
	}
	path := fieldText(imp, "path")
	if canonical := g.aliases[path]; canonical != "" {
		path = canonical
	}
	info := g.globalSymbols[path+"."+fieldText(r, "text")]
	g.noteExternalGlobal(info)
	return info, info != nil
}
