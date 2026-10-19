package parser

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/token"
)

func (p *Parser) parsePattern() *ast.Pattern {
	left := p.parsePatternPrimary()
	for p.at(token.Pipe) && !p.failed {
		p.next()
		p.skipNL()
		right := p.parsePatternPrimary()
		if left.Kind == ast.OrPattern {
			left.Alts = append(left.Alts, right)
			left.Span.End = right.EndPos()
			continue
		}
		left = &ast.Pattern{
			Span: ast.Span{Start: left.Pos(), End: right.EndPos()},
			Kind: ast.OrPattern,
			Alts: []*ast.Pattern{left, right},
		}
	}
	return left
}

func (p *Parser) parsePatternPrimary() *ast.Pattern {
	start := p.cur()
	switch start.Kind {
	case token.Ident:
		p.next()
		if start.Text == "_" {
			return &ast.Pattern{Span: p.emptySpan(start), Kind: ast.WildPattern}
		}
		if p.at(token.LParen) {
			args := p.parsePatternList()
			return &ast.Pattern{
				Span: ast.Span{Start: start.Pos, End: p.prev().End},
				Kind: ast.VariantPattern,
				Name: start.Text,
				Args: args,
			}
		}
		return &ast.Pattern{Span: p.emptySpan(start), Kind: ast.BindPattern, Name: start.Text}
	case token.Int, token.Float, token.Duration, token.Char, token.String, token.Bytes,
		token.KwTrue, token.KwFalse:
		lit := p.parsePatternLiteral()
		return &ast.Pattern{
			Span:    ast.Span{Start: lit.Pos(), End: lit.EndPos()},
			Kind:    ast.LiteralPattern,
			Literal: lit,
		}
	case token.LParen:
		p.next()
		p.enterGroup()
		p.skipNL()
		inner := p.parsePattern()
		p.expect(token.RParen, ")")
		p.leaveGroup()
		return inner
	}
	p.unexpected("a pattern")
	return &ast.Pattern{Span: p.emptySpan(start), Kind: ast.WildPattern}
}

func (p *Parser) parsePatternLiteral() *ast.LiteralExpr {
	tok := p.cur()
	switch tok.Kind {
	case token.Int, token.Float, token.Duration, token.Char, token.String, token.Bytes:
		p.next()
		return &ast.LiteralExpr{Span: p.emptySpan(tok), Kind: tok.Kind, Text: tok.Text, Value: tok.Value}
	case token.KwTrue, token.KwFalse:
		p.next()
		return &ast.LiteralExpr{Span: p.emptySpan(tok), Kind: tok.Kind, Text: tok.Text}
	}
	p.unexpected("a literal pattern")
	return &ast.LiteralExpr{Span: p.emptySpan(tok), Kind: tok.Kind, Text: tok.Text}
}

func (p *Parser) parsePatternList() []*ast.Pattern {
	p.expect(token.LParen, "(")
	p.enterGroup()
	p.skipNL()
	var patterns []*ast.Pattern
	if !p.at(token.RParen) {
		for {
			patterns = append(patterns, p.parsePattern())
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
	return patterns
}
