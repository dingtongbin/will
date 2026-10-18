package willc

import (
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/ast"
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
	case *ast.AggregateExpr:
		return e.emitAggregate(v)
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
	case *ast.SpawnExpr:
		rec := e.add("spawn", v)
		if v.Lane != "" {
			e.text(rec, "lane", v.Lane)
		}
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
	case *ast.LaneHereExpr:
		rec := e.add("name", v)
		e.text(rec, "text", "lane.Here()")
		e.boolean(rec, "builtin", true)
		return rec
	}
	return 0
}

func (e *emitter) emitName(v *ast.NameExpr) int {
	rec := e.add("name", v)
	e.text(rec, "text", v.Name)
	if sym := e.refs[v]; sym != nil {
		if sym.Node != nil {
			e.ref(rec, "to", sym.Node)
		}
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
	return rec
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
		return strings.TrimSuffix(strings.TrimPrefix(v.Text, "'"), "'")
	case token.String:
		return strings.TrimSuffix(strings.TrimPrefix(v.Text, `"`), `"`)
	case token.Bytes:
		return strings.TrimSuffix(strings.TrimPrefix(v.Text, `b"`), `"`)
	}
	return v.Text
}

func (e *emitter) emitCall(c *ast.CallExpr) int {
	if name, ok := c.Callee.(*ast.NameExpr); ok {
		sym := e.refs[name]
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
		if sym != nil && sym.Node != nil {
			e.ref(rec, "to", sym.Node)
		} else {
			to := e.emitNameChild(rec, name)
			e.field(rec, "to", Ref(to))
		}
		e.emitTypeArgsField(rec, c.TypeArgs)
		for _, a := range c.Args {
			aid := e.emitExpr(a.Expr)
			e.field(rec, "arg", Ref(aid))
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
	if special, ok := specialCall(v.Name, len(v.Args)); ok {
		rec := e.add(special, v)
		e.push(rec)
		rid := e.emitExpr(v.Recv)
		switch special {
		case "join":
			e.field(rec, "task", Ref(rid))
		case "send", "trysend":
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
	e.push(rec)
	rid := e.emitExpr(v.Recv)
	e.field(rec, "recv", Ref(rid))
	e.emitTypeArgsField(rec, v.TypeArgs)
	for _, a := range v.Args {
		aid := e.emitExpr(a.Expr)
		e.field(rec, "arg", Ref(aid))
	}
	e.pop()
	return rec
}

func specialCall(name string, args int) (string, bool) {
	switch {
	case name == "Join" && args == 0:
		return "join", true
	case name == "Send" && args == 1:
		return "send", true
	case name == "TrySend" && args == 1:
		return "trysend", true
	case name == "Recv" && args == 0:
		return "recv", true
	case name == "TryRecv" && args == 0:
		return "tryrecv", true
	case name == "Close" && args == 0:
		return "close", true
	}
	return "", false
}

func (e *emitter) emitAggregate(v *ast.AggregateExpr) int {
	rec := e.add("agg", v)
	if target := e.symNode(v); target != nil {
		e.ref(rec, "to", target)
	}
	e.push(rec)
	for _, f := range v.Fields {
		vid := e.emitExpr(f.Value)
		e.field(rec, "field", Text(f.Name+"=#"+strconv.Itoa(vid)))
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
		if len(v.Args) > 1 {
			pid := e.emitExpr(v.Args[1].Expr)
			e.field(rec, "prio", Ref(pid))
		}
		e.pop()
		return rec
	}
	rec := e.add("new", v)
	e.text(rec, "type", typeText(v.Type))
	e.push(rec)
	for _, a := range v.Args {
		aid := e.emitExpr(a.Expr)
		e.field(rec, "arg", Ref(aid))
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
	e.emitBlock(v.Body)
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
