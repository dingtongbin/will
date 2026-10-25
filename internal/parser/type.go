package parser

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

// parseType parses a type expression. A type position always reads <...> as
// type arguments, so no comparison disambiguation is needed here.
func (p *Parser) parseType() *ast.Type {
	start := p.cur()
	switch {
	case p.at(token.KwFunc):
		return p.parseFuncType()
	case p.at(token.KwInterface):
		p.next()
		p.skipNL()
		p.expect(token.LBrace, "{")
		p.skipNL()
		p.expect(token.RBrace, "}")
		return &ast.Type{
			Span: p.spanFrom(start),
			Kind: ast.NamedType,
			Name: "interface {}",
		}
	case p.at(token.LBracket):
		return p.parseArrayType()
	case p.at(token.LParen) && p.peekRaw(1).Kind == token.RParen:
		p.next()
		end := p.next()
		return &ast.Type{Span: ast.Span{Start: start.Pos, End: end.End}, Kind: ast.UnitType}
	case p.at(token.Ident):
		name := p.next().Text
		if p.at(token.Dot) {
			p.next()
			member, _ := p.expectName("a type name")
			name = name + "." + member
		}
		t := &ast.Type{Kind: ast.NamedType, Name: name}
		if isScalarType(name) && !p.at(token.Lt) {
			t.Kind = ast.ScalarType
		}
		if p.at(token.Lt) {
			t.Args = p.parseTypeArgs()
		}
		t.Span = p.spanFrom(start)
		return t
	}
	p.typeUnexpected("a type")
	return &ast.Type{Span: p.emptySpan(start)}
}

// parseTypeArgsInExpr parses type arguments written at a call site. A failure
// here is S0003: the balanced brackets read as a comparison, not as a type
// argument list.
func (p *Parser) parseTypeArgsInExpr() []*ast.Type {
	old := p.inTypeArgs
	p.inTypeArgs = true
	args := p.parseTypeArgs()
	p.inTypeArgs = old
	return args
}

func (p *Parser) parseFuncType() *ast.Type {
	start := p.expect(token.KwFunc, "func")
	p.expect(token.LParen, "(")
	p.enterGroup()
	p.skipNL()
	t := &ast.Type{Kind: ast.FuncType}
	if !p.at(token.RParen) {
		for {
			t.Params = append(t.Params, p.parseType())
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
	if p.at(token.Arrow) {
		p.next()
		p.skipNL()
		t.Result = p.parseType()
	}
	t.Span = p.spanFrom(start)
	return t
}

func (p *Parser) parseArrayType() *ast.Type {
	start := p.expect(token.LBracket, "[")
	if p.at(token.RBracket) {
		p.next()
		elem := p.parseType()
		return &ast.Type{
			Span: ast.Span{Start: start.Pos, End: elem.EndPos()},
			Kind: ast.ViewType,
			Elem: elem,
		}
	}
	if !p.at(token.Int) {
		p.typeUnexpected("an array length")
	}
	length := p.next()
	if !p.at(token.RBracket) {
		p.typeUnexpected("]")
	}
	p.next()
	elem := p.parseType()
	return &ast.Type{
		Span: ast.Span{Start: start.Pos, End: elem.EndPos()},
		Kind: ast.ArrayType,
		Len:  length.Text,
		Elem: elem,
	}
}

func (p *Parser) parseTypeArgs() []*ast.Type {
	p.expect(token.Lt, "<")
	var args []*ast.Type
	p.skipNL()
	if p.atTypeClose() {
		p.consumeTypeClose()
		return args
	}
	for {
		args = append(args, p.parseType())
		if !p.at(token.Comma) {
			break
		}
		p.next()
		p.skipNL()
		if p.atTypeClose() {
			break
		}
	}
	p.consumeTypeClose()
	return args
}

func (p *Parser) atTypeClose() bool {
	k := p.cur().Kind
	return k == token.Gt || k == token.Shr
}

// consumeTypeClose consumes one closing angle bracket. The lexer spells >>
// as one shift token, so the first close splits it into two > tokens.
func (p *Parser) consumeTypeClose() token.Token {
	if p.at(token.Gt) {
		return p.next()
	}
	if p.at(token.Shr) {
		p.splitShr()
		return p.next()
	}
	p.typeUnexpected(">")
	return p.cur()
}

func (p *Parser) splitShr() {
	tok := p.toks[p.pos]
	first := token.Token{
		Kind: token.Gt,
		Text: ">",
		Pos:  tok.Pos,
		End: source.Pos{
			File:   tok.Pos.File,
			Offset: tok.Pos.Offset + 1,
			Line:   tok.Pos.Line,
			Col:    tok.Pos.Col + 1,
		},
	}
	second := token.Token{Kind: token.Gt, Text: ">", Pos: first.End, End: tok.End}
	tail := append([]token.Token(nil), p.toks[p.pos+1:]...)
	p.toks = append(p.toks[:p.pos], first, second)
	p.toks = append(p.toks, tail...)
}

// typeArgsEnd scans for the angle bracket that closes the list starting at
// start. It never crosses a newline; an unclosed list is not type arguments.
func (p *Parser) typeArgsEnd(start int) (int, bool) {
	depth := 0
	for i := start; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case token.Lt:
			depth++
		case token.Gt:
			depth--
		case token.Shr:
			depth -= 2
		case token.Newline, token.EOF:
			return 0, false
		default:
			continue
		}
		if depth == 0 {
			return i, true
		}
		if depth < 0 {
			return 0, false
		}
	}
	return 0, false
}

func isScalarType(name string) bool {
	switch name {
	case "i8", "i16", "i32", "i64", "isize",
		"u8", "u16", "u32", "u64", "usize",
		"f32", "f64", "bool", "str", "raw":
		return true
	}
	return false
}
