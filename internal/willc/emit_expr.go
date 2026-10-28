package willc

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/token"
)

func (e *emitter) emitExpr(x ast.Expr) int {
	switch v := x.(type) {
	case nil:
		return 0
	case *ast.NameExpr:
		return e.emitName(v)
	case *ast.LiteralExpr:
		return e.emitLiteral(v)
	case *ast.CallExpr:
		return e.emitCall(v)
	case *ast.MethodCallExpr:
		return e.emitMethodCall(v)
	case *ast.FieldExpr:
		rec := e.add("field", v)
		e.text(rec, "text", v.Name)
		e.push(rec)
		rid := e.emitExpr(v.Recv)
		e.field(rec, "recv", Ref(rid))
		e.pop()
		return rec
	case *ast.IndexExpr:
		rec := e.add("index", v)
		e.push(rec)
		rid := e.emitExpr(v.Recv)
		e.field(rec, "recv", Ref(rid))
		iid := e.emitExpr(v.Index)
		e.field(rec, "i", Ref(iid))
		e.pop()
		return rec
	case *ast.SliceExpr:
		rec := e.add("slice", v)
		e.push(rec)
		rid := e.emitExpr(v.Recv)
		e.field(rec, "recv", Ref(rid))
		lid := e.emitExpr(v.Lo)
		e.field(rec, "lo", Ref(lid))
		hid := e.emitExpr(v.Hi)
		e.field(rec, "hi", Ref(hid))
		e.pop()
		return rec
	case *ast.BorrowExpr:
		rec := e.add("borrow", v)
		if v.Mut {
			e.boolean(rec, "mut", true)
		}
		e.push(rec)
		xid := e.emitExpr(v.X)
		e.field(rec, "expr", Ref(xid))
		e.pop()
		return rec
	case *ast.AggregateExpr:
		return e.emitAggregate(v)
	case *ast.ArrayLitExpr:
		return e.emitArrayLiteral(v)
	case *ast.NewExpr:
		return e.emitNew(v)
	case *ast.UnaryExpr:
		rec := e.add("un", v)
		e.text(rec, "op", v.Op.String())
		e.push(rec)
		xid := e.emitExpr(v.X)
		e.field(rec, "expr", Ref(xid))
		e.pop()
		return rec
	case *ast.BinaryExpr:
		rec := e.add("bin", v)
		e.text(rec, "op", v.Op.String())
		e.push(rec)
		lid := e.emitExpr(v.L)
		e.field(rec, "l", Ref(lid))
		rid := e.emitExpr(v.R)
		e.field(rec, "r", Ref(rid))
		e.pop()
		return rec
	case *ast.RangeExpr:
		rec := e.add("range", v)
		e.push(rec)
		lid := e.emitExpr(v.Lo)
		e.field(rec, "lo", Ref(lid))
		hid := e.emitExpr(v.Hi)
		e.field(rec, "hi", Ref(hid))
		e.pop()
		return rec
	case *ast.LambdaExpr:
		return e.emitLambda(v)
	case *ast.TryExpr:
		rec := e.add("try", v)
		e.push(rec)
		xid := e.emitExpr(v.X)
		e.field(rec, "expr", Ref(xid))
		e.pop()
		return rec
	case *ast.UnwrapExpr:
		rec := e.add("unwrap", v)
		e.push(rec)
		xid := e.emitExpr(v.X)
		e.field(rec, "expr", Ref(xid))
		e.pop()
		return rec
	case *ast.AwaitExpr:
		rec := e.add("await", v)
		e.push(rec)
		xid := e.emitExpr(v.X)
		e.field(rec, "expr", Ref(xid))
		e.pop()
		return rec
	case *ast.SpawnExpr:
		rec := e.add("spawn", v)
		e.push(rec)
		cid := e.emitExpr(v.Call)
		e.field(rec, "call", Ref(cid))
		e.pop()
		return rec
	case *ast.DeadlineExpr:
		return e.emitDeadline(v)
	case *ast.IfExpr:
		return e.emitIf(v)
	case *ast.MatchExpr:
		return e.emitMatch(v)
	case *ast.InterpExpr:
		return e.emitInterp(v)
	case *ast.UnitExpr:
		rec := e.add("lit", v)
		e.word(rec, "kind", "unit")
		e.text(rec, "text", "()")
		e.text(rec, "type", "()")
		return rec
	}
	return 0
}

func (e *emitter) emitName(v *ast.NameExpr) int {
	rec := e.add("name", v)
	e.text(rec, "text", v.Name)
	if t := e.nameTypes[v]; t != "" {
		e.text(rec, "type", t)
	}
	sym := e.refs[v]
	if sym != nil {
		e.refSym(rec, "to", sym)
		if sym.Predeclared {
			e.boolean(rec, "builtin", true)
		}
	}
	return rec
}

func (e *emitter) emitLiteral(v *ast.LiteralExpr) int {
	rec := e.add("lit", v)
	e.word(rec, "kind", litKind(v.Kind))
	e.text(rec, "text", literalText(v))
	e.text(rec, "type", e.literalTypeText(v))
	return rec
}

// literalTypeText is the resolved type of a literal. The checker records it
// once the context is known, so the backend never infers a width; without a
// recorded type the default of the kind applies. A character is a u32 Unicode
// scalar value, which is also what the checker records.
func (e *emitter) literalTypeText(v *ast.LiteralExpr) string {
	if t := e.litTypes[v]; t != "" {
		return t
	}
	switch v.Kind {
	case token.Int:
		return "i32"
	case token.Float:
		return "f64"
	case token.Char:
		return "u32"
	case token.String:
		return "str"
	case token.Bytes:
		return "[]u8"
	case token.Duration:
		return "Duration"
	case token.KwTrue, token.KwFalse:
		return "bool"
	}
	return ""
}

func litKind(k token.Kind) string {
	switch k {
	case token.Int:
		return "int"
	case token.Float:
		return "float"
	case token.Char:
		return "char"
	case token.String:
		return "str"
	case token.Bytes:
		return "bytes"
	case token.Duration:
		return "duration"
	case token.KwTrue, token.KwFalse:
		return "bool"
	}
	return "int"
}

// literalText returns the source spelling without its delimiters.
func literalText(v *ast.LiteralExpr) string {
	switch v.Kind {
	case token.Char:
		r, _ := utf8.DecodeRuneInString(v.Value)
		return strconv.Itoa(int(r))
	case token.String:
		return strings.TrimSuffix(strings.TrimPrefix(v.Text, `"`), `"`)
	case token.Bytes:
		return strings.TrimSuffix(strings.TrimPrefix(v.Text, `b"`), `"`)
	}
	return v.Text
}

func (e *emitter) emitCallArg(rec int, a *ast.CallArg) {
	if a.Name != "" {
		e.text(rec, "name", a.Name)
	}
	aid := e.emitExpr(a.Expr)
	e.field(rec, "arg", Ref(aid))
}

func (e *emitter) emitCall(c *ast.CallExpr) int {
	if name, ok := c.Callee.(*ast.NameExpr); ok {
		sym := e.refs[name]
		if sym != nil && sym.Kind == resolve.StructKind {
			if decl, ok := sym.Node.(*ast.StructDecl); ok {
				if decl.Class {
					return e.emitClassNew(c, decl.Name)
				}
				return e.emitStructNew(c, decl.Name)
			}
		}
		if sym != nil && sym.Predeclared && (name.Name == "panic" || name.Name == "assert") {
			rec := e.add(name.Name, c)
			e.push(rec)
			for _, a := range c.Args {
				aid := e.emitExpr(a.Expr)
				e.field(rec, "arg", Ref(aid))
			}
			e.pop()
			return rec
		}
		if sym != nil && sym.IsType() && len(c.Args) == 1 && len(c.TypeArgs) == 0 {
			rec := e.add("cast", c)
			e.push(rec)
			if sym.Node != nil {
				e.ref(rec, "to", sym.Node)
			} else {
				to := e.emitType(&ast.Type{Kind: ast.NamedType, Name: name.Name})
				e.field(rec, "to", Ref(to))
			}
			aid := e.emitExpr(c.Args[0].Expr)
			e.field(rec, "expr", Ref(aid))
			e.pop()
			return rec
		}
		rec := e.add("call", c)
		e.push(rec)
		if t := e.callTypes[c]; t != "" {
			e.text(rec, "type", t)
		}
		if sym != nil && sym.Node != nil {
			e.refSym(rec, "to", sym)
		} else {
			to := e.emitNameChild(rec, name)
			e.field(rec, "to", Ref(to))
		}
		e.emitTypeArgsField(rec, c.TypeArgs)
		for _, a := range c.Args {
			e.emitCallArg(rec, a)
		}
		e.pop()
		return rec
	}
	rec := e.add("call", c)
	e.push(rec)
	cid := e.emitExpr(c.Callee)
	e.field(rec, "to", Ref(cid))
	e.emitTypeArgsField(rec, c.TypeArgs)
	for _, a := range c.Args {
		e.emitCallArg(rec, a)
	}
	e.pop()
	return rec
}

// emitClassNew writes a class constructor call. T(...) allocates one GC
// object; init, when present, is invoked by the class lowering after field
// defaults are written.
func (e *emitter) emitClassNew(c *ast.CallExpr, name string) int {
	return e.emitNewArgs(c, c.Args, name)
}

func (e *emitter) emitNewArgs(node ast.Node, args []*ast.CallArg, name string) int {
	rec := e.add("new", node)
	e.text(rec, "type", name)
	e.push(rec)
	for _, a := range args {
		if a.Name != "" {
			e.text(rec, "name", a.Name)
		}
		aid := e.emitExpr(a.Expr)
		e.field(rec, "arg", Ref(aid))
	}
	e.pop()
	return rec
}

// emitStructNew writes a value-type constructor call. The backend builds the
// value in a slot, applies defaults, and runs init when the type declares one.
func (e *emitter) emitStructNew(c *ast.CallExpr, name string) int {
	rec := e.add("new", c)
	e.text(rec, "type", name)
	e.push(rec)
	for _, a := range c.Args {
		if a.Name != "" {
			e.text(rec, "name", a.Name)
		}
		aid := e.emitExpr(a.Expr)
		e.field(rec, "arg", Ref(aid))
	}
	e.pop()
	return rec
}

// emitNameChild writes the callee name as a child node and returns its id.
func (e *emitter) emitNameChild(rec int, name *ast.NameExpr) int {
	id := e.add("name", name)
	e.text(id, "text", name.Name)
	return id
}

func (e *emitter) emitTypeArgsField(rec int, types []*ast.Type) {
	for _, ta := range types {
		tid := e.emitType(ta)
		e.field(rec, "typearg", Ref(tid))
	}
}

func (e *emitter) emitMethodCall(v *ast.MethodCallExpr) int {
	if recv, ok := v.Recv.(*ast.NameExpr); ok {
		if sym := e.refs[recv]; sym != nil && sym.Kind == resolve.ImportKind {
			imp, _ := sym.Node.(*ast.Import)
			if imp != nil {
				path := imp.Path
				if canonical := e.aliases[path]; canonical != "" {
					path = canonical
				}
				if decl := e.external[path+"."+v.Name]; decl != nil && decl.Class {
					return e.emitNewArgs(v, v.Args, path+"."+v.Name)
				}
			}
		}
	}
	if special, ok := specialCall(v.Name, len(v.Args)); ok {
		rec := e.add(special, v)
		e.push(rec)
		rid := e.emitExpr(v.Recv)
		switch special {
		case "send":
			e.field(rec, "chan", Ref(rid))
			aid := e.emitExpr(v.Args[0].Expr)
			e.field(rec, "value", Ref(aid))
		default:
			e.field(rec, "chan", Ref(rid))
		}
		e.pop()
		return rec
	}
	rec := e.add("mcall", v)
	e.text(rec, "text", v.Name)
	if sym := e.refs[v]; sym != nil && sym.Predeclared {
		e.boolean(rec, "builtin", true)
	}
	// A method reached through a package qualifier is named by its package,
	// because its declaration is not a node of this file.
	if recv, ok := v.Recv.(*ast.NameExpr); ok {
		if qualified := e.qualifiedImport(e.refs[recv], v.Name); qualified != "" {
			e.text(rec, "to", qualified)
		}
	}
	e.push(rec)
	rid := e.emitExpr(v.Recv)
	e.field(rec, "recv", Ref(rid))
	e.emitTypeArgsField(rec, v.TypeArgs)
	for _, a := range v.Args {
		e.emitCallArg(rec, a)
	}
	e.pop()
	return rec
}

func specialCall(name string, args int) (string, bool) {
	switch {
	case name == "send" && args == 1:
		return "send", true
	case name == "recv" && args == 0:
		return "recv", true
	}
	return "", false
}

func (e *emitter) emitAggregate(v *ast.AggregateExpr) int {
	rec := e.add("agg", v)
	if sym := e.refs[v]; sym != nil && sym.Kind == resolve.ImportKind {
		if qualified := e.qualifiedImport(sym, memberName(v.TypeName)); qualified != "" {
			e.text(rec, "to", qualified)
		}
	} else if target := e.symNode(v); target != nil {
		if id, ok := e.ids[target]; ok {
			e.field(rec, "to", Ref(id))
		} else if qualified := e.qualified(e.refs[v]); qualified != "" {
			e.text(rec, "to", qualified)
		}
	}
	e.push(rec)
	e.emitTypeArgsField(rec, v.TypeArgs)
	for _, f := range v.Fields {
		vid := e.emitExpr(f.Value)
		e.field(rec, "field", Text(f.Name+"=#"+strconv.Itoa(vid)))
	}
	e.pop()
	return rec
}

func (e *emitter) emitArrayLiteral(v *ast.ArrayLitExpr) int {
	rec := e.add("arrlit", v)
	e.text(rec, "type", typeText(v.Type))
	e.push(rec)
	for _, elem := range v.Elems {
		id := e.emitExpr(elem)
		e.field(rec, "elem", Ref(id))
	}
	e.pop()
	return rec
}

func (e *emitter) emitNew(v *ast.NewExpr) int {
	if v.Type != nil && v.Type.Kind == ast.NamedType && v.Type.Name == "chan" {
		rec := e.add("channew", v)
		e.text(rec, "type", typeText(v.Type))
		e.push(rec)
		if len(v.Args) > 0 {
			aid := e.emitExpr(v.Args[0].Expr)
			e.field(rec, "cap", Ref(aid))
		}
		e.pop()
		return rec
	}
	rec := e.add("new", v)
	e.text(rec, "type", typeText(v.Type))
	e.push(rec)
	for _, a := range v.Args {
		e.emitCallArg(rec, a)
	}
	e.pop()
	return rec
}

func (e *emitter) emitLambda(v *ast.LambdaExpr) int {
	rec := e.add("lambda", v)
	if v.Result != nil {
		e.text(rec, "type", typeText(v.Result))
	}
	e.push(rec)
	e.emitParams(v.Params)
	e.emitType(v.Result)
	e.ref(rec, "body", v.Body)
	// A closure body runs later and owns a copy of every capture. It must not
	// release an outer owner at its own block end.
	oldScopes, oldLoops := e.scopes, e.loops
	oldTypes := e.localTypes
	e.scopes = [][]ast.Node{{}}
	e.loops = nil
	e.localTypes = map[string]*ast.Type{}
	for _, p := range v.Params {
		e.localTypes[p.Name] = p.Type
	}
	e.emitBlock(v.Body)
	e.scopes, e.loops, e.localTypes = oldScopes, oldLoops, oldTypes
	e.pop()
	return rec
}

func (e *emitter) emitInterp(v *ast.InterpExpr) int {
	rec := e.add("interp", v)
	e.push(rec)
	for _, part := range v.Parts {
		if part.Expr != nil {
			id := e.emitExpr(part.Expr)
			e.field(rec, "part", Ref(id))
		} else {
			e.field(rec, "part", Text(part.Lit))
		}
	}
	e.pop()
	return rec
}
