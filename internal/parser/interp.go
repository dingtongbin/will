package parser

import (
	"unicode/utf8"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/lexer"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

// parseInterp splits an interpolated literal into literal text and expression
// parts. The lexer has already checked that the literal closes and that every
// escape in literal text is defined.
func (p *Parser) parseInterp(tok token.Token) ast.Expr {
	raw := tok.Value
	interp := &ast.InterpExpr{Span: p.emptySpan(tok)}
	baseLine := tok.Pos.Line
	baseCol := tok.Pos.Col + 2
	baseOff := tok.Pos.Offset + 2
	at := func(i int) source.Pos {
		return source.Pos{File: tok.Pos.File, Offset: baseOff + i, Line: baseLine, Col: baseCol + i}
	}

	litStart := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '{':
			if i+1 < len(raw) && raw[i+1] == '{' {
				i++
				continue
			}
			p.addInterpLiteral(interp, raw[litStart:i], at(litStart), at(i))
			close := matchInterpBrace(raw, i)
			if close < 0 {
				p.bag.Error(at(i), diag.CodeUnexpectedToken, "unexpected token", "expected }")
				p.failed = true
				return interp
			}
			expr := p.parseInterpExpr(raw[i+1:close], at(i+1))
			interp.Parts = append(interp.Parts, ast.InterpPart{
				Span: ast.Span{Start: at(i), End: at(close + 1)},
				Expr: expr,
			})
			i = close
			litStart = close + 1
		case '}':
			if i+1 < len(raw) && raw[i+1] == '}' {
				i++
			}
		}
	}
	if litStart < len(raw) {
		p.addInterpLiteral(interp, raw[litStart:], at(litStart), at(len(raw)))
	}
	return interp
}

func (p *Parser) addInterpLiteral(interp *ast.InterpExpr, raw string, start, end source.Pos) {
	if raw == "" {
		return
	}
	interp.Parts = append(interp.Parts, ast.InterpPart{
		Span: ast.Span{Start: start, End: end},
		Lit:  decodeInterpLiteral(raw),
	})
}

func (p *Parser) parseInterpExpr(raw string, pos source.Pos) ast.Expr {
	if raw == "" {
		p.bag.Error(pos, diag.CodeUnexpectedToken, "unexpected token", "expected an expression")
		p.failed = true
		return &ast.NameExpr{Span: ast.Span{Start: pos, End: pos}}
	}
	toks, ds := lexer.ScanAt(p.file, []byte(raw+"\n"), pos)
	for _, d := range ds {
		p.bag.Add(d)
	}
	if len(ds) > 0 {
		p.failed = true
		return &ast.NameExpr{Span: ast.Span{Start: pos, End: pos}}
	}
	sub := &Parser{file: p.file, toks: toks}
	x := sub.parseExpr(0, true)
	if !sub.failed && !sub.at(token.Newline) && !sub.at(token.EOF) {
		sub.unexpected("end of interpolation")
	}
	for _, d := range sub.bag.Diagnostics() {
		p.bag.Add(d)
	}
	if sub.failed {
		p.failed = true
	}
	return x
}

// matchInterpBrace finds the } that closes the { at open. Quoted literals and
// comments inside the expression are skipped so their braces do not count.
func matchInterpBrace(raw string, open int) int {
	depth := 1
	for i := open + 1; i < len(raw); i++ {
		switch raw[i] {
		case '"', '\'':
			i = skipInterpQuote(raw, i)
			if i < 0 {
				return -1
			}
		case '/':
			if i+1 < len(raw) && raw[i+1] == '/' {
				return -1
			}
			if i+1 < len(raw) && raw[i+1] == '*' {
				end := skipInterpBlockComment(raw, i)
				if end < 0 {
					return -1
				}
				i = end
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func skipInterpQuote(raw string, open int) int {
	quote := raw[open]
	for i := open + 1; i < len(raw); i++ {
		if raw[i] == '\\' {
			i++
			continue
		}
		if raw[i] == quote {
			return i
		}
	}
	return -1
}

func skipInterpBlockComment(raw string, open int) int {
	for i := open + 2; i+1 < len(raw); i++ {
		if raw[i] == '*' && raw[i+1] == '/' {
			return i + 1
		}
	}
	return -1
}

func decodeInterpLiteral(raw string) string {
	var out []byte
	for i := 0; i < len(raw); {
		if raw[i] == '{' && i+1 < len(raw) && raw[i+1] == '{' {
			out = append(out, '{')
			i += 2
			continue
		}
		if raw[i] == '}' && i+1 < len(raw) && raw[i+1] == '}' {
			out = append(out, '}')
			i += 2
			continue
		}
		if raw[i] != '\\' {
			out = append(out, raw[i])
			i++
			continue
		}
		value, next := decodeInterpEscape(raw, i)
		if next < 0 {
			out = append(out, raw[i])
			i++
			continue
		}
		out = append(out, value...)
		i = next
	}
	return string(out)
}

func decodeInterpEscape(raw string, start int) ([]byte, int) {
	if start+1 >= len(raw) {
		return nil, -1
	}
	switch raw[start+1] {
	case 'n':
		return []byte{'\n'}, start + 2
	case 'r':
		return []byte{'\r'}, start + 2
	case 't':
		return []byte{'\t'}, start + 2
	case '0':
		return []byte{0}, start + 2
	case '\\':
		return []byte{'\\'}, start + 2
	case '"':
		return []byte{'"'}, start + 2
	case '\'':
		return []byte{'\''}, start + 2
	case 'x':
		return decodeInterpHex(raw, start+2)
	case 'u':
		return decodeInterpUnicode(raw, start+2)
	}
	return nil, -1
}

func decodeInterpHex(raw string, start int) ([]byte, int) {
	if start+1 >= len(raw) {
		return nil, -1
	}
	hi := hexValue(raw[start])
	lo := hexValue(raw[start+1])
	if hi < 0 || lo < 0 {
		return nil, -1
	}
	return []byte{byte(hi*16 + lo)}, start + 2
}

func decodeInterpUnicode(raw string, start int) ([]byte, int) {
	if start >= len(raw) || raw[start] != '{' {
		return nil, -1
	}
	var value rune
	digits := 0
	i := start + 1
	for ; i < len(raw) && raw[i] != '}'; i++ {
		v := hexValue(raw[i])
		if v < 0 || digits == 6 {
			return nil, -1
		}
		value = value*16 + rune(v)
		digits++
	}
	if i >= len(raw) || digits == 0 || !utf8.ValidRune(value) {
		return nil, -1
	}
	return []byte(string(value)), i + 1
}

func hexValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}
