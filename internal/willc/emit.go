package willc

import (
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/resolve"
)

// Emit renders one resolved file as a willc document, followed by the
// diagnostics of this run.
//
// The emitter assigns ids in the same preorder it writes, so a reference to a
// node that has not been written yet is patched after the walk.
func Emit(f *ast.File, res *resolve.Result, modulePath string, diags []diag.Diagnostic, litTypes map[*ast.LiteralExpr]string, callTypes map[*ast.CallExpr]string, nameTypes map[*ast.NameExpr]string, aliases map[string]string, external ...map[string]*ast.StructDecl) ([]byte, error) {
	var externalStructs map[string]*ast.StructDecl
	if len(external) != 0 {
		externalStructs = external[0]
	}
	e := &emitter{
		ids:         map[ast.Node]int{},
		refs:        res.Refs,
		modulePath:  modulePath,
		litTypes:    litTypes,
		callTypes:   callTypes,
		nameTypes:   nameTypes,
		aliases:     aliases,
		released:    map[ast.Node]bool{},
		moved:       map[ast.Node]bool{},
		leaked:      map[ast.Node]bool{},
		structs:     collectStructs(f),
		external:    externalStructs,
		funcs:       collectFuncs(f),
		viewReturns: collectViewReturns(f),
		localTypes:  map[string]*ast.Type{},
	}
	root := e.add("module", f)
	e.text(root, "path", modulePath)
	e.push(root)
	for _, imp := range f.Imports {
		e.emitImport(imp)
	}
	for _, d := range f.Decls {
		e.emitDecl(d)
	}
	e.emitDiagnostics(diags)
	e.pop()
	e.add("end", nil)
	if err := e.patchRefs(); err != nil {
		return nil, err
	}
	return Write(&e.doc), nil
}

type emitter struct {
	doc        Doc
	ids        map[ast.Node]int
	refs       map[ast.Node]*resolve.Symbol
	parents    []int
	pending    []pendingRef
	modulePath string
	// litTypes is the resolved type text of every literal, by node. A literal
	// whose type the checker did not record takes the default of its kind.
	litTypes map[*ast.LiteralExpr]string
	// callTypes and nameTypes are the resolved type texts of the predeclared
	// constructors and values, by node.
	callTypes map[*ast.CallExpr]string
	nameTypes map[*ast.NameExpr]string
	// aliases maps an import path that aliases a package to the package's
	// canonical path, so a qualified name matches the declaring package.
	aliases map[string]string
	// scopes stacks the heap-owning locals declared so far in each open block;
	// loops records the scope depth of every enclosing loop body.
	scopes   [][]ast.Node
	loops    []int
	released map[ast.Node]bool
	moved    map[ast.Node]bool
	leaked   map[ast.Node]bool
	// structs holds the local struct declarations, so a field read can be
	// classified as a scalar/rc copy, a view, or an owning value.
	structs map[string]*ast.StructDecl
	// external holds struct declarations from other files or packages, keyed by
	// their short and package-qualified names. Imported fields need the same
	// ownership classification as local ones.
	external map[string]*ast.StructDecl
	// funcs holds the local free functions. A scalar-returning local call with
	// no callback parameter cannot hand back a borrow of its argument.
	funcs map[string]*ast.FuncDecl
	// viewReturns maps a local function to the parameter index whose slice it
	// returns as a view. It lets a call result participate in the same view
	// lifetime as a direct slice.
	viewReturns map[string]int
	// localTypes is the type of every local and parameter currently visible.
	localTypes map[string]*ast.Type
}

// collectViewReturns records local functions that return a view of one named
// parameter. A function with a return from a local owner, an unknown
// expression, or a different parameter stays out of the table and therefore
// keeps the caller's owner pinned.
func collectViewReturns(f *ast.File) map[string]int {
	out := map[string]int{}
	if f == nil {
		return out
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || fn.Result == nil || fn.Result.Kind != ast.ViewType {
			continue
		}
		index := map[string]int{}
		for i, p := range fn.Params {
			index[p.Name] = i
		}
		owner := -1
		good := true
		var walkStmt func(ast.Stmt)
		walkStmt = func(st ast.Stmt) {
			switch s := st.(type) {
			case *ast.Block:
				for _, inner := range s.Stmts {
					walkStmt(inner)
				}
			case *ast.ReturnStmt:
				root := viewRootOwner(s.Value)
				i, ok := index[root]
				if !ok || (owner != -1 && owner != i) {
					good = false
					return
				}
				owner = i
			case *ast.ForStmt:
				walkStmt(s.Body)
			case *ast.RegionStmt:
				walkStmt(s.Body)
			case *ast.ParallelStmt:
				walkStmt(s.Body)
			case *ast.IfExpr:
				walkStmt(s.Then)
				if s.Else != nil {
					walkStmt(s.Else)
				}
			case *ast.MatchExpr:
				for _, arm := range s.Arms {
					walkStmt(arm.Body)
				}
			case *ast.SelectStmt:
				for _, arm := range s.Arms {
					walkStmt(arm.Body)
				}
			case *ast.DeadlineExpr:
				walkStmt(s.Body)
			}
		}
		walkStmt(fn.Body)
		if good && owner >= 0 {
			out[fn.Name] = owner
		}
	}
	return out
}

func collectFuncs(f *ast.File) map[string]*ast.FuncDecl {
	out := map[string]*ast.FuncDecl{}
	if f == nil {
		return out
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
			out[fn.Name] = fn
		}
	}
	return out
}

func collectStructs(f *ast.File) map[string]*ast.StructDecl {
	out := map[string]*ast.StructDecl{}
	if f == nil {
		return out
	}
	for _, d := range f.Decls {
		if s, ok := d.(*ast.StructDecl); ok {
			out[s.Name] = s
		}
	}
	return out
}

type pendingRef struct {
	rec    int
	field  int
	target ast.Node
}

func (e *emitter) add(kind string, node ast.Node) int {
	id := len(e.doc.Records) + 1
	rec := Record{ID: id, Kind: kind}
	if len(e.parents) > 0 {
		rec.Fields = append(rec.Fields, Field{Name: "parent", Value: Ref(e.parents[len(e.parents)-1])})
	}
	if node != nil {
		rec.Fields = append(rec.Fields, Field{Name: "pos", Value: Text(node.Pos().String())})
		e.ids[node] = id
	}
	e.doc.Records = append(e.doc.Records, rec)
	return id
}

func (e *emitter) push(id int) { e.parents = append(e.parents, id) }
func (e *emitter) pop()        { e.parents = e.parents[:len(e.parents)-1] }

// addPos writes a record whose position comes from node but whose id does not
// replace the node's own id, which a release record needs: it points at the
// block it belongs to while its owner is another node.
func (e *emitter) addPos(kind string, node ast.Node) int {
	id := len(e.doc.Records) + 1
	rec := Record{ID: id, Kind: kind}
	if len(e.parents) > 0 {
		rec.Fields = append(rec.Fields, Field{Name: "parent", Value: Ref(e.parents[len(e.parents)-1])})
	}
	if node != nil {
		rec.Fields = append(rec.Fields, Field{Name: "pos", Value: Text(node.Pos().String())})
	}
	e.doc.Records = append(e.doc.Records, rec)
	return id
}

func (e *emitter) last() int { return len(e.doc.Records) - 1 }

func (e *emitter) field(rec int, name string, v Value) {
	idx := rec - 1
	e.doc.Records[idx].Fields = append(e.doc.Records[idx].Fields, Field{Name: name, Value: v})
}

func (e *emitter) text(rec int, name, s string)       { e.field(rec, name, Text(s)) }
func (e *emitter) word(rec int, name, s string)       { e.field(rec, name, Word(s)) }
func (e *emitter) number(rec int, name string, n int) { e.field(rec, name, Int(n)) }
func (e *emitter) boolean(rec int, name string, b bool) {
	e.field(rec, name, Bool(b))
}

// ref points a field at an already written node, or records the target for
// patching when the target is written later.
func (e *emitter) ref(rec int, name string, target ast.Node) {
	if target == nil {
		return
	}
	if id, ok := e.ids[target]; ok {
		e.field(rec, name, Ref(id))
		return
	}
	e.field(rec, name, Ref(0))
	e.pending = append(e.pending, pendingRef{rec: rec, field: len(e.doc.Records[rec-1].Fields) - 1, target: target})
}

// symNode returns the node a resolved name points at, or nil for a built-in
// symbol that has no declaration.
func (e *emitter) symNode(node ast.Node) ast.Node {
	if sym := e.refs[node]; sym != nil {
		return sym.Node
	}
	return nil
}

// refSym points a field at a node of this file. A declaration that lives
// elsewhere, in another file of the same package, is written as its
// package-qualified name instead, because a node id only means something
// inside one file.
func (e *emitter) refSym(rec int, name string, sym *resolve.Symbol) {
	if sym == nil {
		return
	}
	if sym.Node != nil {
		if id, ok := e.ids[sym.Node]; ok {
			e.field(rec, name, Ref(id))
			return
		}
		if qualified := e.qualified(sym); qualified != "" {
			e.field(rec, name, Text(qualified))
			return
		}
		e.ref(rec, name, sym.Node)
	}
}

// qualified returns the package-qualified name of a symbol declared outside
// this file, or "" when the symbol has no such name.
func (e *emitter) qualified(sym *resolve.Symbol) string {
	if sym == nil || e.modulePath == "" {
		return ""
	}
	switch sym.Kind {
	case resolve.MethodKind:
		if sym.RecvType != "" {
			return e.modulePath + "." + sym.RecvType + "." + sym.Name
		}
		return e.modulePath + "." + sym.Name
	case resolve.ImportKind, resolve.PredeclaredTypeKind, resolve.PredeclaredValueKind:
		return ""
	}
	return e.modulePath + "." + sym.Name
}

// qualifiedImport returns the package-qualified name of a member reached
// through an import qualifier, or "" when sym is not a package import. A C
// import binds no package member, so it has no such name.
func (e *emitter) qualifiedImport(sym *resolve.Symbol, member string) string {
	if sym == nil || sym.Kind != resolve.ImportKind {
		return ""
	}
	imp, ok := sym.Node.(*ast.Import)
	if !ok || imp.C || member == "" {
		return ""
	}
	path := imp.Path
	if canonical := e.aliases[path]; canonical != "" {
		path = canonical
	}
	return path + "." + member
}

// memberName returns the part of a qualified name after its last dot.
func memberName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func (e *emitter) patchRefs() error {
	for _, p := range e.pending {
		id, ok := e.ids[p.target]
		if !ok || id == 0 {
			return &Error{p.rec, "cannot translate node #" + strconv.Itoa(p.rec)}
		}
		e.doc.Records[p.rec-1].Fields[p.field].Value = Ref(id)
	}
	return nil
}

// emitDiagnostics writes one diagnostic node per message and the diagnostic
// count, so a cache entry with only diagnostics still records what was found.
func (e *emitter) emitDiagnostics(ds []diag.Diagnostic) {
	for _, d := range ds {
		rec := e.add("diagnostic", nil)
		if d.Pos.IsValid() {
			e.text(rec, "pos", d.Pos.String())
		}
		if d.End.IsValid() {
			e.text(rec, "end", d.End.String())
		}
		e.text(rec, "code", d.Code)
		e.word(rec, "severity", d.Severity.String())
		e.text(rec, "message", d.Message)
		if d.Help != "" {
			e.text(rec, "help", d.Help)
		}
		for _, n := range d.Notes {
			if n.Pos.IsValid() {
				e.text(rec, "note", n.Pos.String()+": "+n.Message)
			} else {
				e.text(rec, "note", n.Message)
			}
		}
	}
	if len(ds) > 0 {
		rec := e.add("count", nil)
		e.text(rec, "name", "diagnostics")
		e.number(rec, "n", len(ds))
	}
}

func (e *emitter) emitImport(imp *ast.Import) {
	kind := "import"
	if imp.C {
		kind = "cimport"
	}
	rec := e.add(kind, imp)
	e.text(rec, "path", imp.Path)
	if imp.Alias != "" {
		e.text(rec, "alias", imp.Alias)
	}
	if imp.SideEffect {
		e.boolean(rec, "unused", true)
	}
	if !imp.C {
		return
	}
	e.push(rec)
	for _, link := range imp.Links {
		id := e.add("link", link)
		e.text(id, "text", link.Name)
		if link.Fast {
			e.boolean(id, "fast", true)
		}
		if link.Blocking {
			e.boolean(id, "blocking", true)
		}
		if link.Typed {
			e.boolean(id, "typed", true)
			e.text(id, "type", ast.TypeText(link.Result))
			for _, p := range link.Params {
				e.text(id, "param", ast.TypeText(p))
			}
			if link.Variadic {
				e.boolean(id, "variadic", true)
			}
		}
	}
	e.pop()
}

func (e *emitter) emitDecl(d ast.Decl) {
	switch decl := d.(type) {
	case *ast.ConstDecl:
		rec := e.add("const", decl)
		e.text(rec, "text", decl.Name)
		e.text(rec, "type", typeText(decl.Type))
		e.push(rec)
		e.emitType(decl.Type)
		e.emitExpr(decl.Value)
		e.pop()
	case *ast.VarDecl:
		rec := e.add("var", decl)
		e.text(rec, "text", decl.Name)
		e.text(rec, "type", typeText(decl.Type))
		e.push(rec)
		e.emitType(decl.Type)
		e.emitExpr(decl.Value)
		e.pop()
	case *ast.TypeAliasDecl:
		rec := e.add("typealias", decl)
		e.text(rec, "text", decl.Name)
		e.ref(rec, "to", decl.Type)
		e.push(rec)
		e.emitType(decl.Type)
		e.pop()
	case *ast.FuncDecl:
		e.emitFunc(decl)
	case *ast.StructDecl:
		rec := e.add("struct", decl)
		e.text(rec, "text", decl.Name)
		e.push(rec)
		e.emitTypeParams(decl.TypeParams)
		for _, f := range decl.Fields {
			fid := e.add("field", f)
			e.text(fid, "text", f.Name)
			e.text(fid, "type", typeText(f.Type))
			e.push(fid)
			e.emitType(f.Type)
			if f.Default != nil {
				e.ref(fid, "default", f.Default)
				e.emitExpr(f.Default)
			}
			e.pop()
		}
		e.pop()
	case *ast.EnumDecl:
		rec := e.add("enum", decl)
		e.text(rec, "text", decl.Name)
		e.push(rec)
		e.emitTypeParams(decl.TypeParams)
		for _, v := range decl.Variants {
			vid := e.add("variant", v)
			e.text(vid, "text", v.Name)
			e.push(vid)
			for _, at := range v.Args {
				tid := e.emitType(at)
				e.field(vid, "arg", Ref(tid))
			}
			e.pop()
		}
		e.pop()
	case *ast.InterfaceDecl:
		rec := e.add("interface", decl)
		e.text(rec, "text", decl.Name)
		e.push(rec)
		e.emitTypeParams(decl.TypeParams)
		for _, m := range decl.Methods {
			mid := e.add("method-sig", m)
			e.text(mid, "text", m.Name)
			e.text(mid, "type", typeText(m.Result))
			if m.Async {
				e.boolean(mid, "async", true)
			}
			e.push(mid)
			e.emitParams(m.Params)
			e.emitType(m.Result)
			for _, src := range m.From {
				id := e.emitExpr(src)
				e.field(mid, "from", Ref(id))
			}
			e.pop()
		}
		e.pop()
	case *ast.DeriveDecl:
		rec := e.add("derive", decl)
		e.text(rec, "text", decl.Name)
		e.ref(rec, "to", decl.Type)
		e.push(rec)
		e.emitType(decl.Type)
		e.pop()
	case *ast.LaneDecl:
		rec := e.add("lane", decl)
		e.text(rec, "text", decl.Name)
		if decl.Decl != nil {
			e.ref(rec, "to", decl.Decl)
		}
		e.push(rec)
		if decl.Decl != nil {
			e.emitDecl(decl.Decl)
		}
		e.pop()
	}
}

func (e *emitter) emitFunc(fn *ast.FuncDecl) {
	oldTypes := e.localTypes
	e.localTypes = map[string]*ast.Type{}
	if fn.Recv != nil {
		e.localTypes[fn.Recv.Name] = fn.Recv.Type
	}
	for _, p := range fn.Params {
		e.localTypes[p.Name] = p.Type
	}
	defer func() { e.localTypes = oldTypes }()
	rec := e.add("func", fn)
	e.text(rec, "text", fn.Name)
	if fn.Result != nil {
		e.text(rec, "type", typeText(fn.Result))
	}
	if fn.Async {
		e.boolean(rec, "async", true)
	}
	e.push(rec)
	if fn.Recv != nil {
		rid := e.add("receiver", fn.Recv)
		e.text(rid, "text", fn.Recv.Name)
		e.text(rid, "type", typeText(fn.Recv.Type))
		e.push(rid)
		e.emitType(fn.Recv.Type)
		e.pop()
	}
	e.emitTypeParams(fn.TypeParams)
	e.emitParams(fn.Params)
	e.emitType(fn.Result)
	for _, src := range fn.From {
		id := e.emitExpr(src)
		e.field(rec, "from", Ref(id))
	}
	e.emitBlock(fn.Body)
	e.pop()
}

func (e *emitter) emitTypeParams(params []*ast.TypeParam) {
	for _, tp := range params {
		id := e.add("typeparam", tp)
		e.text(id, "text", tp.Name)
		if len(tp.Constraints) > 0 {
			parts := make([]string, len(tp.Constraints))
			for i, c := range tp.Constraints {
				parts[i] = typeText(c)
			}
			e.text(id, "constraint", strings.Join(parts, "+"))
		}
		e.push(id)
		for _, c := range tp.Constraints {
			e.emitType(c)
		}
		e.pop()
	}
}

func (e *emitter) emitParams(params []*ast.Param) {
	for _, pm := range params {
		id := e.add("param", pm)
		e.text(id, "text", pm.Name)
		e.text(id, "type", typeText(pm.Type))
		if pm.Variadic {
			e.boolean(id, "variadic", true)
		}
		e.push(id)
		e.emitType(pm.Type)
		if pm.Default != nil {
			did := e.emitExpr(pm.Default)
			e.field(id, "default", Ref(did))
		}
		e.pop()
	}
}

func (e *emitter) emitType(t *ast.Type) int {
	if t == nil {
		return 0
	}
	rec := e.add("type", t)
	e.text(rec, "text", typeText(t))
	e.word(rec, "kind", typeKind(t))
	if t.Kind == ast.ViewType && t.ViewMut {
		e.boolean(rec, "mut", true)
	}
	e.typeRef(rec, t)
	e.push(rec)
	switch t.Kind {
	case ast.NamedType:
		for _, a := range t.Args {
			id := e.emitType(a)
			e.field(rec, "arg", Ref(id))
		}
	case ast.ArrayType, ast.ViewType:
		id := e.emitType(t.Elem)
		e.field(rec, "arg", Ref(id))
	case ast.FuncType:
		for _, p := range t.Params {
			id := e.emitType(p)
			e.field(rec, "arg", Ref(id))
		}
		if t.Result != nil {
			id := e.emitType(t.Result)
			e.field(rec, "arg", Ref(id))
		}
	}
	e.pop()
	return rec
}

// typeRef writes the to field of a type record. A type declared in this file
// is a node reference; one declared in another file of the package, or in an
// imported package, is written as its package-qualified name.
func (e *emitter) typeRef(rec int, t *ast.Type) {
	sym := e.refs[t]
	if sym == nil {
		return
	}
	if sym.Kind == resolve.ImportKind {
		if qualified := e.qualifiedImport(sym, memberName(t.Name)); qualified != "" {
			e.text(rec, "to", qualified)
		}
		return
	}
	if sym.Kind == resolve.PredeclaredTypeKind {
		return
	}
	e.refSym(rec, "to", sym)
}

// typeText renders a type for a willc field; the spelling comes from the
// intermediate format's one renderer.
func typeText(t *ast.Type) string {
	return ast.TypeText(t)
}

func typeKind(t *ast.Type) string {
	switch t.Borrow {
	case ast.RefBorrow:
		return "ref"
	case ast.MutBorrow:
		return "mutref"
	}
	switch t.Kind {
	case ast.ScalarType:
		return "scalar"
	case ast.ArrayType:
		return "array"
	case ast.ViewType:
		return "view"
	case ast.FuncType:
		return "func"
	case ast.UnitType:
		return "unit"
	}
	switch t.Name {
	case "Option":
		return "option"
	case "Result":
		return "result"
	case "rc":
		return "rc"
	case "arc":
		return "arc"
	case "weak_rc":
		return "weak_rc"
	case "weak_arc":
		return "weak_arc"
	case "chan":
		return "chan"
	case "Task":
		return "task"
	}
	return "named"
}
