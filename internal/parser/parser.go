// Package parser turns will tokens into a syntax tree. It reports S0001,
// S0002, and S0003 with the fixed texts from the diagnostic table.
package parser

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/lexer"
	"github.com/will-lang/will/internal/token"
)

// Parse lexes and parses one source file. A file with lexical errors is not
// parsed; its lexical diagnostics are returned unchanged.
func Parse(file string, src []byte) (*ast.File, []diag.Diagnostic) {
	toks, ds := lexer.Scan(file, src)
	if len(ds) > 0 {
		return nil, ds
	}
	p := &Parser{file: file, toks: toks}
	f := p.parseFile()
	return f, p.bag.Diagnostics()
}

// Parser holds one token stream and the diagnostics found so far.
type Parser struct {
	file       string
	toks       []token.Token
	pos        int
	bag        diag.Bag
	ignoreNL   int
	inTypeArgs bool
	failed     bool
}

func (p *Parser) cur() token.Token {
	if p.ignoreNL > 0 {
		for p.pos < len(p.toks)-1 && p.toks[p.pos].Kind == token.Newline {
			p.pos++
		}
	}
	return p.toks[p.pos]
}

func (p *Parser) peekRaw(n int) token.Token {
	i := p.pos + n
	if i >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[i]
}

func (p *Parser) prev() token.Token {
	if p.pos == 0 {
		return p.toks[0]
	}
	return p.toks[p.pos-1]
}

func (p *Parser) at(k token.Kind) bool {
	return p.cur().Kind == k
}

func (p *Parser) atText(text string) bool {
	tok := p.cur()
	return tok.Kind == token.Ident && tok.Text == text
}

func (p *Parser) next() token.Token {
	tok := p.cur()
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return tok
}

func (p *Parser) skipNL() {
	for p.pos < len(p.toks) && p.toks[p.pos].Kind == token.Newline {
		p.pos++
	}
}

func (p *Parser) skipTrivia() {
	for {
		switch p.toks[p.pos].Kind {
		case token.Newline, token.DocComment:
			p.pos++
		default:
			return
		}
	}
}

func (p *Parser) expect(k token.Kind, production string) token.Token {
	if p.at(k) {
		return p.next()
	}
	p.unexpected(production)
	return p.cur()
}

func (p *Parser) expectText(text, production string) token.Token {
	if p.atText(text) {
		return p.next()
	}
	p.unexpected(production)
	return p.cur()
}

func (p *Parser) expectName(production string) (string, token.Token) {
	tok := p.cur()
	if tok.Kind == token.Ident {
		p.next()
		return tok.Text, tok
	}
	if tok.Kind.IsKeyword() {
		p.bag.Report(tok.Pos, diag.KeywordName)
		p.failed = true
		p.next()
		return "", tok
	}
	p.unexpected(production)
	return "", tok
}

func (p *Parser) unexpected(production string) {
	if p.failed {
		return
	}
	p.bag.Error(p.cur().Pos, diag.CodeUnexpectedToken, "unexpected token", "expected "+production)
	p.failed = true
}

// typeUnexpected reports a malformed type. Inside a call-site type argument
// list the failure means the list cannot be parsed as type arguments.
func (p *Parser) typeUnexpected(production string) {
	if p.failed {
		return
	}
	if p.inTypeArgs {
		p.bag.Report(p.cur().Pos, diag.TypeArguments)
		p.failed = true
		return
	}
	p.unexpected(production)
}

func (p *Parser) spanFrom(start token.Token) ast.Span {
	end := p.prev()
	if end.End.Offset < start.Pos.Offset {
		end = start
	}
	return ast.Span{Start: start.Pos, End: end.End}
}

func (p *Parser) emptySpan(tok token.Token) ast.Span {
	return ast.Span{Start: tok.Pos, End: tok.End}
}

// endStatement consumes the separating newline after a statement or
// declaration. A closing brace and EOF also end the statement.
func (p *Parser) endStatement() {
	if p.failed || p.ignoreNL > 0 {
		return
	}
	switch p.cur().Kind {
	case token.Newline:
		for p.cur().Kind == token.Newline {
			p.next()
		}
	case token.RBrace, token.EOF:
	default:
		p.unexpected("a newline or }")
	}
}

// enterGroup marks a region where newlines carry no meaning.
func (p *Parser) enterGroup() {
	p.ignoreNL++
}

// leaveGroup restores the previous newline significance.
func (p *Parser) leaveGroup() {
	if p.ignoreNL > 0 {
		p.ignoreNL--
	}
}
