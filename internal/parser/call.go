package parser

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/token"
)

func (p *Parser) parseCall(callee ast.Expr, typeArgs []*ast.Type) ast.Expr {
	args := p.parseCallArgs()
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

func (p *Parser) parseIndex(x ast.Expr) ast.Expr {
	p.expect(token.LBracket, "[")
	p.enterGroup()
	p.skipNL()
	lo := p.parseExpr(precRange+1, true)
	if p.at(token.DotDot) {
		p.next()
		p.skipNL()
		hi := p.parseExpr(precRange+1, true)
		p.expect(token.RBracket, "]")
		p.leaveGroup()
		return &ast.SliceExpr{
			Span: ast.Span{Start: x.Pos(), End: p.prev().End},
			Recv: x,
			Lo:   lo,
			Hi:   hi,
		}
	}
	p.expect(token.RBracket, "]")
	p.leaveGroup()
	return &ast.IndexExpr{
		Span:  ast.Span{Start: x.Pos(), End: p.prev().End},
		Recv:  x,
		Index: lo,
	}
}
