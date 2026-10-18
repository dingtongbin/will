package willc

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
)

func (e *emitter) emitBlock(b *ast.Block) int {
	if b == nil {
		return 0
	}
	rec := e.add("block", b)
	e.push(rec)
	for _, s := range b.Stmts {
		e.emitStmt(s)
	}
	e.pop()
	return rec
}

func (e *emitter) emitStmt(s ast.Stmt) int {
	switch st := s.(type) {
	case *ast.Block:
		return e.emitBlock(st)
	case *ast.VarStmt:
		switch d := st.Decl.(type) {
		case *ast.ConstDecl:
			rec := e.add("const", d)
			e.text(rec, "text", d.Name)
			e.text(rec, "type", typeText(d.Type))
			e.push(rec)
			e.emitType(d.Type)
			e.emitExpr(d.Value)
			e.pop()
			return rec
		case *ast.VarDecl:
			rec := e.add("var", d)
			e.text(rec, "text", d.Name)
			e.text(rec, "type", typeText(d.Type))
			e.push(rec)
			e.emitType(d.Type)
			e.emitExpr(d.Value)
			e.pop()
			return rec
		}
	case *ast.AssignStmt:
		if isDiscard(st) {
			return e.emitDiscard(st)
		}
		rec := e.add("assign", st)
		e.text(rec, "op", st.Op)
		e.push(rec)
		e.ref(rec, "target", st.Target)
		e.emitExpr(st.Target)
		e.ref(rec, "value", st.Value)
		e.emitExpr(st.Value)
		e.pop()
		return rec
	case *ast.ExprStmt:
		rec := e.add("exprstmt", st)
		e.push(rec)
		e.ref(rec, "expr", st.Expr)
		e.emitExpr(st.Expr)
		e.pop()
		return rec
	case *ast.DeferStmt:
		rec := e.add("defer", st)
		e.push(rec)
		e.ref(rec, "call", st.Call)
		e.emitExpr(st.Call)
		e.pop()
		return rec
	case *ast.ReturnStmt:
		rec := e.add("return", st)
		if st.Value != nil {
			e.push(rec)
			e.ref(rec, "value", st.Value)
			e.emitExpr(st.Value)
			e.pop()
		}
		return rec
	case *ast.BreakStmt:
		return e.add("break", st)
	case *ast.ContinueStmt:
		return e.add("continue", st)
	case *ast.ForStmt:
		rec := e.add("for", st)
		for _, name := range st.Names {
			e.text(rec, "bind", name)
		}
		e.push(rec)
		if st.Iter != nil {
			e.ref(rec, "iter", st.Iter)
			e.emitExpr(st.Iter)
		}
		e.ref(rec, "body", st.Body)
		e.emitBlock(st.Body)
		e.pop()
		return rec
	case *ast.RegionStmt:
		rec := e.add("region", st)
		e.push(rec)
		e.ref(rec, "body", st.Body)
		e.emitBlock(st.Body)
		e.pop()
		return rec
	case *ast.ParallelStmt:
		rec := e.add("parallel", st)
		e.push(rec)
		e.ref(rec, "body", st.Body)
		e.emitBlock(st.Body)
		e.pop()
		return rec
	case *ast.SelectStmt:
		rec := e.add("select", st)
		e.push(rec)
		for _, arm := range st.Arms {
			e.emitSelectArm(arm)
		}
		e.pop()
		return rec
	case *ast.IfExpr:
		return e.emitIf(st)
	case *ast.MatchExpr:
		return e.emitMatch(st)
	case *ast.DeadlineExpr:
		return e.emitDeadline(st)
	}
	return 0
}

func isDiscard(st *ast.AssignStmt) bool {
	if st.Op != "=" {
		return false
	}
	name, ok := st.Target.(*ast.NameExpr)
	return ok && name.Name == "_"
}

func (e *emitter) emitDiscard(st *ast.AssignStmt) int {
	rec := e.add("exprstmt", st)
	e.push(rec)
	discard := e.add("discard", st)
	e.push(discard)
	value := e.emitExpr(st.Value)
	e.field(discard, "expr", Ref(value))
	e.pop()
	e.field(rec, "expr", Ref(discard))
	e.pop()
	return rec
}

func (e *emitter) emitIf(ifx *ast.IfExpr) int {
	rec := e.add("if", ifx)
	e.push(rec)
	e.ref(rec, "cond", ifx.Cond)
	e.emitExpr(ifx.Cond)
	e.ref(rec, "then", ifx.Then)
	e.emitBlock(ifx.Then)
	if ifx.Else != nil {
		e.ref(rec, "else", ifx.Else)
		e.emitStmt(ifx.Else)
	}
	e.pop()
	return rec
}

func (e *emitter) emitMatch(m *ast.MatchExpr) int {
	rec := e.add("match", m)
	e.push(rec)
	e.ref(rec, "subject", m.Subject)
	e.emitExpr(m.Subject)
	for _, arm := range m.Arms {
		e.emitMatchArm(arm)
	}
	e.pop()
	return rec
}

func (e *emitter) emitMatchArm(arm *ast.MatchArm) int {
	rec := e.add("arm", arm)
	e.push(rec)
	e.ref(rec, "pattern", arm.Pattern)
	e.emitPattern(arm.Pattern)
	if arm.Guard != nil {
		e.ref(rec, "guard", arm.Guard)
		e.emitExpr(arm.Guard)
	}
	e.ref(rec, "body", arm.Body)
	e.emitStmt(arm.Body)
	e.pop()
	return rec
}

func (e *emitter) emitSelectArm(arm *ast.SelectArm) int {
	rec := e.add("selarm", arm)
	if arm.Bind != "" {
		e.text(rec, "bind", arm.Bind)
	}
	if arm.Default {
		e.boolean(rec, "default", true)
	}
	e.push(rec)
	if arm.Chan != nil {
		e.ref(rec, "chan", arm.Chan)
		e.emitExpr(arm.Chan)
	}
	if arm.Timeout != nil {
		e.ref(rec, "timeout", arm.Timeout)
		e.emitExpr(arm.Timeout)
	}
	e.ref(rec, "body", arm.Body)
	e.emitStmt(arm.Body)
	e.pop()
	return rec
}

func (e *emitter) emitDeadline(d *ast.DeadlineExpr) int {
	rec := e.add("deadline", d)
	e.push(rec)
	e.ref(rec, "d", d.Duration)
	e.emitExpr(d.Duration)
	e.ref(rec, "body", d.Body)
	e.emitBlock(d.Body)
	e.pop()
	return rec
}

func (e *emitter) emitPattern(p *ast.Pattern) int {
	if p == nil {
		return 0
	}
	rec := e.add("pattern", p)
	e.text(rec, "text", patternText(p))
	e.word(rec, "kind", patternKind(p))
	if p.Kind == ast.VariantPattern {
		if target := e.symNode(p); target != nil {
			e.ref(rec, "to", target)
		}
	}
	e.push(rec)
	for _, a := range p.Args {
		e.emitPattern(a)
	}
	for _, a := range p.Alts {
		e.emitPattern(a)
	}
	e.pop()
	return rec
}

func patternKind(p *ast.Pattern) string {
	switch p.Kind {
	case ast.WildPattern:
		return "wild"
	case ast.BindPattern:
		return "bind"
	case ast.LiteralPattern:
		return "literal"
	case ast.RangePattern:
		return "range"
	case ast.VariantPattern:
		return "variant"
	case ast.OrPattern:
		return "or"
	}
	return "wild"
}

func patternText(p *ast.Pattern) string {
	switch p.Kind {
	case ast.WildPattern:
		return "_"
	case ast.BindPattern:
		return p.Name
	case ast.LiteralPattern:
		if p.Literal != nil {
			return literalText(p.Literal)
		}
	case ast.RangePattern:
		return literalText(p.Lo) + ".." + literalText(p.Hi)
	case ast.VariantPattern:
		if len(p.Args) == 0 {
			return p.Name
		}
		parts := make([]string, len(p.Args))
		for i, a := range p.Args {
			parts[i] = patternText(a)
		}
		return p.Name + "(" + strings.Join(parts, ",") + ")"
	case ast.OrPattern:
		parts := make([]string, len(p.Alts))
		for i, a := range p.Alts {
			parts[i] = patternText(a)
		}
		return strings.Join(parts, " | ")
	}
	return ""
}
