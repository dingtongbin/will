package parser

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/token"
)

// parseCall parses a call. Range(lo, hi) is the constructor of the predefined
// half-open range type, so it becomes a range expression; any other spelling
// stays a call.
func (p *Parser) parseCall(callee ast.Expr, typeArgs []*ast.Type) ast.Expr {
	args := p.parseCallArgs()
	if len(typeArgs) == 0 && len(args) == 2 && !args[0].Spread && !args[1].Spread {
		if name, ok := callee.(*ast.NameExpr); ok && name.Name == "Range" {
			return &ast.RangeExpr{
				Span: ast.Span{Start: callee.Pos(), End: p.prev().End},
				Lo:   args[0].Expr,
				Hi:   args[1].Expr,
			}
		}
	}
	return &ast.CallExpr{
		Span:     ast.Span{Start: callee.Pos(), End: p.prev().End},
		Callee:   callee,
		TypeArgs: typeArgs,
		Args:     args,
	}
}

func (p *Parser) parseCallArgs() []*ast.CallArg {
	p.expect(token.LParen, "(")
	p.enterGroup()
	p.skipNL()
	var args []*ast.CallArg
	if !p.at(token.RParen) {
		for {
			start := p.cur()
			x := p.parseExpr(0, true)
			arg := &ast.CallArg{Expr: x}
			if p.at(token.DotDotDot) {
				p.next()
				arg.Spread = true
			}
			arg.Span = p.spanFrom(start)
			args = append(args, arg)
			if !p.at(token.Comma) {
				break
			}
			p.next()
			p.skipNL()
			if p.at(token.RParen) {
				break
			}
		}
	}
	p.expect(token.RParen, ")")
	p.leaveGroup()
	return args
}

// parseIndex parses xs[i] and, when the index is a range, the slice
// xs[Range(lo, hi)].
func (p *Parser) parseIndex(x ast.Expr) ast.Expr {
	p.expect(token.LBracket, "[")
	p.enterGroup()
	p.skipNL()
	index := p.parseExpr(0, true)
	p.expect(token.RBracket, "]")
	p.leaveGroup()
	span := ast.Span{Start: x.Pos(), End: p.prev().End}
	if r, ok := index.(*ast.RangeExpr); ok {
		return &ast.SliceExpr{Span: span, Recv: x, Lo: r.Lo, Hi: r.Hi}
	}
	return &ast.IndexExpr{Span: span, Recv: x, Index: index}
}
