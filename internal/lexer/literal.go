package lexer

import (
	"unicode/utf8"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

func (l *Lexer) scanChar() {
	startOff := l.off
	start := l.pos()
	l.advance()

	if l.off >= len(l.src) || l.src[l.off] == '\n' {
		l.report(start, diag.UnterminatedString)
		l.emit(token.Char, start, startOff)
		return
	}
	if l.src[l.off] == '\'' {
		l.report(start, diag.BadChar)
		l.advance()
		l.emit(token.Char, start, startOff)
		return
	}

	var value []byte
	if l.src[l.off] == '\\' {
		value = l.scanEscape(false)
	} else {
		_, size := utf8.DecodeRune(l.src[l.off:])
		value = append(value, l.src[l.off:l.off+size]...)
		for i := 0; i < size; i++ {
			l.advance()
		}
	}

	if l.off >= len(l.src) || l.src[l.off] == '\n' {
		l.report(start, diag.UnterminatedString)
		l.emitChar(start, startOff, value)
		return
	}
	if l.src[l.off] != '\'' {
		end := l.off
		for end < len(l.src) && l.src[end] != '\'' && l.src[end] != '\n' {
			end++
		}
		if end < len(l.src) && l.src[end] == '\'' {
			l.report(start, diag.BadChar)
			for l.off <= end {
				l.advance()
			}
		} else {
			l.report(start, diag.UnterminatedString)
			for l.off < len(l.src) && l.src[l.off] != '\n' {
				l.advance()
			}
		}
		l.emitChar(start, startOff, value)
		return
	}
	l.advance()
	l.emitChar(start, startOff, value)
}

func (l *Lexer) emitChar(start source.Pos, startOff int, value []byte) {
	l.emit(token.Char, start, startOff)
	l.setValue(string(value))
}

func (l *Lexer) scanQuoted(kind token.Kind, prefix int) {
	startOff := l.off
	start := l.pos()
	for i := 0; i < prefix; i++ {
		l.advance()
	}
	l.advance()
	rawStart := l.off

	if kind == token.FString {
		closed, rawEnd := l.scanFStringBody()
		if !closed {
			l.report(start, diag.UnterminatedString)
		}
		l.emit(kind, start, startOff)
		l.setValue(string(l.src[rawStart:rawEnd]))
		return
	}

	var value []byte
	for {
		if l.off >= len(l.src) || l.src[l.off] == '\n' {
			l.report(start, diag.UnterminatedString)
			l.emit(kind, start, startOff)
			l.setValue(string(value))
			return
		}
		c := l.src[l.off]
		if c == '"' {
			l.advance()
			l.emit(kind, start, startOff)
			l.setValue(string(value))
			return
		}
		if c == '\\' {
			value = append(value, l.scanEscape(kind == token.Bytes)...)
			continue
		}
		_, size := utf8.DecodeRune(l.src[l.off:])
		value = append(value, l.src[l.off:l.off+size]...)
		for i := 0; i < size; i++ {
			l.advance()
		}
	}
}

// scanFStringBody consumes an interpolated literal up to its closing quote.
// Literal text is validated as it goes; an interpolation is skipped as a
// balanced brace region so quotes inside it do not end the outer literal.
func (l *Lexer) scanFStringBody() (bool, int) {
	depth := 0
	for l.off < len(l.src) && l.src[l.off] != '\n' {
		c := l.src[l.off]
		if depth == 0 {
			switch c {
			case '"':
				end := l.off
				l.advance()
				return true, end
			case '\\':
				l.scanEscape(false)
			case '{':
				if l.peek(1) == '{' {
					l.advance()
					l.advance()
				} else {
					depth = 1
					l.advance()
				}
			case '}':
				l.advance()
				if l.peek(0) == '}' {
					l.advance()
				}
			default:
				l.advance()
			}
			continue
		}
		switch c {
		case '{':
			depth++
			l.advance()
		case '}':
			depth--
			l.advance()
		case '"', '\'':
			l.skipNestedQuote(c)
		case '/':
			if l.peek(1) == '/' {
				for l.off < len(l.src) && l.src[l.off] != '\n' {
					l.advance()
				}
			} else if l.peek(1) == '*' {
				l.skipNestedBlockComment()
			} else {
				l.advance()
			}
		default:
			l.advance()
		}
	}
	return false, l.off
}

func (l *Lexer) skipNestedQuote(quote byte) {
	l.advance()
	for l.off < len(l.src) && l.src[l.off] != '\n' {
		c := l.src[l.off]
		if c == '\\' {
			l.scanEscape(false)
			continue
		}
		l.advance()
		if c == quote {
			return
		}
	}
}

func (l *Lexer) skipNestedBlockComment() {
	start := l.pos()
	l.advance()
	l.advance()
	for l.off < len(l.src) {
		if l.src[l.off] == '*' && l.peek(1) == '/' {
			l.advance()
			l.advance()
			return
		}
		l.advance()
	}
	l.report(start, diag.UnterminatedBlockComment)
}

func (l *Lexer) setValue(value string) {
	l.toks[len(l.toks)-1].Value = value
}

func (l *Lexer) scanEscape(byteString bool) []byte {
	escPos := l.pos()
	l.advance()
	if l.off >= len(l.src) || l.src[l.off] == '\n' {
		l.report(escPos, diag.UnknownEscape)
		return nil
	}
	c := l.src[l.off]
	switch c {
	case 'n':
		l.advance()
		return []byte{'\n'}
	case 'r':
		l.advance()
		return []byte{'\r'}
	case 't':
		l.advance()
		return []byte{'\t'}
	case '0':
		l.advance()
		return []byte{0}
	case '\\':
		l.advance()
		return []byte{'\\'}
	case '"':
		l.advance()
		return []byte{'"'}
	case '\'':
		l.advance()
		return []byte{'\''}
	case 'x':
		l.advance()
		return l.scanHexEscape(escPos)
	case 'u':
		l.advance()
		if byteString {
			l.report(escPos, diag.UnknownEscape)
			l.skipUnicodeEscape()
			return nil
		}
		return l.scanUnicodeEscape(escPos)
	default:
		l.report(escPos, diag.UnknownEscape)
		_, size := utf8.DecodeRune(l.src[l.off:])
		for i := 0; i < size; i++ {
			l.advance()
		}
		return nil
	}
}

func (l *Lexer) scanHexEscape(escPos source.Pos) []byte {
	var value byte
	for i := 0; i < 2; i++ {
		v := digitValue(l.peek(0))
		if v < 0 || v >= 16 {
			l.report(escPos, diag.UnknownEscape)
			return nil
		}
		value = value*16 + byte(v)
		l.advance()
	}
	return []byte{value}
}

func (l *Lexer) scanUnicodeEscape(escPos source.Pos) []byte {
	if l.peek(0) != '{' {
		l.report(escPos, diag.UnknownEscape)
		l.skipUnicodeEscape()
		return nil
	}
	l.advance()
	var digits int
	var value rune
	for {
		c := l.peek(0)
		if c == '}' {
			l.advance()
			break
		}
		v := digitValue(c)
		if v < 0 || v >= 16 {
			l.report(escPos, diag.UnknownEscape)
			l.skipUnicodeEscape()
			return nil
		}
		digits++
		if digits > 6 {
			l.report(escPos, diag.UnknownEscape)
			l.skipUnicodeEscape()
			return nil
		}
		value = value*16 + rune(v)
		l.advance()
	}
	if digits == 0 || !utf8.ValidRune(value) {
		l.report(escPos, diag.UnknownEscape)
		return nil
	}
	buf := make([]byte, utf8.RuneLen(value))
	utf8.EncodeRune(buf, value)
	return buf
}

func (l *Lexer) skipUnicodeEscape() {
	if l.peek(0) != '{' {
		for i := 0; i < 4 && digitValue(l.peek(0)) >= 0; i++ {
			l.advance()
		}
		return
	}
	for l.off < len(l.src) && l.src[l.off] != '}' && l.src[l.off] != '\n' {
		l.advance()
	}
	if l.peek(0) == '}' {
		l.advance()
	}
}
