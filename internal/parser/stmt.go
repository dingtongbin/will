package parser

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/token"
)

func (p *Parser) parseBlock(production string) *ast.Block {
	start := p.expect(token.LBrace, production)
	old := p.ignoreNL
	p.ignoreNL = 0
	b := &ast.Block{}
	p.skipNL()
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
		p.skipDocComments()
		before := p.pos
		b.Stmts = append(b.Stmts, p.parseStmt())
		p.endStatement()
		if p.pos == before && !p.failed {
			p.next()
		}
	}
	p.expect(token.RBrace, "}")
	p.ignoreNL = old
	b.Span = p.spanFrom(start)
	return b
}

func (p *Parser) skipDocComments() {
	for p.toks[p.pos].Kind == token.DocComment {
		p.pos++
		for p.toks[p.pos].Kind == token.Newline {
			p.pos++
		}
	}
}

func (p *Parser) parseStmt() ast.Stmt {
	switch p.cur().Kind {
	case token.KwVar, token.KwConst:
		d := p.parseValueDecl("")
		return &ast.VarStmt{Span: ast.Span{Start: d.Pos(), End: d.EndPos()}, Decl: d}
	case token.KwIf:
		return p.parseIf()
	case token.KwFor:
		return p.parseFor()
	case token.KwMatch:
		return p.parseMatch()
	case token.KwSelect:
		return p.parseSelect()
	case token.KwRegion:
		start := p.next()
		body := p.parseBlock("a region body")
		return &ast.RegionStmt{Span: p.spanFrom(start), Body: body}
	case token.KwParallel:
		start := p.next()
		body := p.parseBlock("a parallel body")
		return &ast.ParallelStmt{Span: p.spanFrom(start), Body: body}
	case token.KwReturn:
		return p.parseReturn()
	case token.KwBreak:
		start := p.next()
		return &ast.BreakStmt{Span: p.spanFrom(start)}
	case token.KwContinue:
		start := p.next()
		return &ast.ContinueStmt{Span: p.spanFrom(start)}
	case token.KwDefer:
		return p.parseDefer()
	case token.KwDeadline:
		return p.parseDeadline()
	}
	return p.parseExprStmt()
}

func (p *Parser) parseExprStmt() ast.Stmt {
	start := p.cur()
	x := p.parseExpr(0, true)
	if p.failed {
		return &ast.ExprStmt{Span: p.spanFrom(start), Expr: x}
	}
	if op, ok := assignOp(p.cur().Kind); ok {
		p.next()
		p.skipNL()
		value := p.parseExpr(0, true)
		p.checkWritable(x, op)
		return &ast.AssignStmt{
			Span:   ast.Span{Start: x.Pos(), End: value.EndPos()},
			Target: x,
			Op:     op,
			Value:  value,
		}
	}
	if !isCallLike(x) && !p.atBlockEnd() {
		p.bag.Error(x.Pos(), diag.CodeUnexpectedToken, "unexpected token", "expected a call or spawn")
		p.failed = true
	}
	return &ast.ExprStmt{Span: ast.Span{Start: start.Pos, End: x.EndPos()}, Expr: x}
}

// atBlockEnd reports whether the next non-newline token closes the block. The
// last expression of a block is its value, so it need not be a call.
func (p *Parser) atBlockEnd() bool {
	i := p.pos
	for i < len(p.toks) && p.toks[i].Kind == token.Newline {
		i++
	}
	return p.toks[i].Kind == token.RBrace
}

func (p *Parser) checkWritable(x ast.Expr, op string) {
	switch target := x.(type) {
	case *ast.NameExpr:
		if target.Name == "_" {
			if op != "=" {
				p.bag.Error(target.Pos(), diag.CodeUnexpectedToken, "unexpected token", "expected a writable place")
				p.failed = true
			}
			return
		}
		return
	case *ast.FieldExpr, *ast.IndexExpr:
		return
	}
	p.bag.Error(x.Pos(), diag.CodeUnexpectedToken, "unexpected token", "expected a writable place")
	p.failed = true
}

func isCallLike(x ast.Expr) bool {
	switch x.(type) {
	case *ast.CallExpr, *ast.MethodCallExpr, *ast.SpawnExpr:
		return true
	}
	return false
}

func assignOp(k token.Kind) (string, bool) {
	switch k {
	case token.Assign:
		return "=", true
	case token.AddAssign:
		return "+=", true
	case token.SubAssign:
		return "-=", true
	case token.MulAssign:
		return "*=", true
	case token.DivAssign:
		return "/=", true
	case token.ModAssign:
		return "%=", true
	case token.AndAssign:
		return "&=", true
	case token.OrAssign:
		return "|=", true
	case token.XorAssign:
		return "^=", true
	case token.ShlAssign:
		return "<<=", true
	case token.ShrAssign:
		return ">>=", true
	case token.PlusWrapAssign:
		return "+%=", true
	case token.MinusWrapAssign:
		return "-%=", true
	case token.StarWrapAssign:
		return "*%=", true
	case token.PlusSatAssign:
		return "+|=", true
	case token.MinusSatAssign:
		return "-|=", true
	case token.StarSatAssign:
		return "*|=", true
	case token.ShlWrapAssign:
		return "<<%=", true
	}
	return "", false
}

func (p *Parser) parseFor() *ast.ForStmt {
	start := p.next()
	f := &ast.ForStmt{}
	if !p.at(token.LBrace) {
		p.skipNL()
		for {
			name, _ := p.expectName("a loop binding")
			f.Names = append(f.Names, name)
			if !p.at(token.Comma) {
				break
			}
			p.next()
			p.skipNL()
		}
		p.expect(token.KwIn, "in")
		p.skipNL()
		f.Iter = p.parseExpr(0, false)
	}
	f.Body = p.parseBlock("a loop body")
	f.Span = p.spanFrom(start)
	return f
}

func (p *Parser) parseReturn() *ast.ReturnStmt {
	start := p.next()
	r := &ast.ReturnStmt{}
	switch p.cur().Kind {
	case token.Newline, token.RBrace, token.EOF:
	default:
		r.Value = p.parseExpr(0, true)
	}
	r.Span = p.spanFrom(start)
	return r
}

func (p *Parser) parseDefer() *ast.DeferStmt {
	start := p.next()
	p.skipNL()
	call := p.parseExpr(0, true)
	if !isCallLike(call) {
		p.bag.Error(call.Pos(), diag.CodeUnexpectedToken, "unexpected token", "expected a call")
		p.failed = true
	}
	return &ast.DeferStmt{Span: p.spanFrom(start), Call: call}
}

func (p *Parser) parseSelect() *ast.SelectStmt {
	start := p.next()
	p.expect(token.LBrace, "{")
	old := p.ignoreNL
	p.ignoreNL = 0
	sel := &ast.SelectStmt{}
	p.skipNL()
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
		sel.Arms = append(sel.Arms, p.parseSelectArm())
		p.endStatement()
	}
	p.expect(token.RBrace, "}")
	p.ignoreNL = old
	sel.Span = p.spanFrom(start)
	return sel
}

func (p *Parser) parseSelectArm() *ast.SelectArm {
	start := p.cur()
	arm := &ast.SelectArm{}
	switch {
	case p.atText("default"):
		arm.Default = true
		p.next()
		p.expect(token.FatArrow, "=>")
	case p.atText("timeout"):
		p.next()
		p.skipNL()
		arm.Timeout = p.parseExpr(0, true)
		p.expect(token.FatArrow, "=>")
	case p.cur().Kind == token.Ident && p.peekRaw(1).Kind == token.Assign:
		arm.Bind = p.next().Text
		p.next()
		p.skipNL()
		arm.Chan = p.parseExpr(0, true)
		p.expect(token.FatArrow, "=>")
	default:
		arm.Chan = p.parseExpr(0, true)
		p.expect(token.FatArrow, "=>")
	}
	arm.Body = p.parseArmBody()
	arm.Span = p.spanFrom(start)
	return arm
}

func (p *Parser) parseArmBody() ast.Stmt {
	if p.at(token.LBrace) {
		return p.parseBlock("a block")
	}
	x := p.parseExpr(0, true)
	return &ast.ExprStmt{Span: ast.Span{Start: x.Pos(), End: x.EndPos()}, Expr: x}
}
