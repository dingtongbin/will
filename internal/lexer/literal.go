// Package lexer turns will source bytes into tokens and lexical diagnostics.
package lexer

import (
	"fmt"
	"unicode/utf8"

	"github.com/will-lang/will/internal/token"
)

func (l *Lexer) scanChar() {
	startOff := l.off
	start := l.pos()
	l.advance()
	if l.peek(0) == '\'' {
		l.advance()
		l.errorAt(start, CodeBadChar, "empty character literal", "write exactly one character between the quotes")
		l.emit(token.Char, start, startOff)
		return
	}
	if l.off >= len(l.src) || l.src[l.off] == '\n' {
		l.errorAt(start, CodeUnterminatedChar, "unterminated character literal", "close the character literal with '")
		l.emit(token.Char, start, startOff)
		return
	}
	var value []byte
	if l.src[l.off] == '\\' {
		value = l.scanEscape(false)
	} else {
		r, size := utf8.DecodeRune(l.src[l.off:])
		if r == utf8.RuneError && size == 1 {
			l.errorAt(l.pos(), CodeInvalidChar, "invalid UTF-8 in character literal", "write ASCII text or a \\u escape")
		}
		value = append(value, l.src[l.off:l.off+size]...)
		for i := 0; i < size; i++ {
			l.advance()
		}
	}
	if l.off >= len(l.src) || l.src[l.off] == '\n' {
		l.errorAt(start, CodeUnterminatedChar, "unterminated character literal", "close the character literal with '")
		l.emit(token.Char, start, startOff)
		l.setValue(string(value))
		return
	}
	if l.peek(0) != '\'' {
		l.errorAt(start, CodeBadChar, "character literal must contain exactly one character", "write exactly one character between the quotes")
		for l.off < len(l.src) && l.src[l.off] != '\'' && l.src[l.off] != '\n' {
			l.advance()
		}
	}
	if l.peek(0) == '\'' {
		l.advance()
	}
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
	var value []byte
	for {
		if l.off >= len(l.src) || l.src[l.off] == '\n' {
			if kind == token.FString {
				l.errorAt(start, CodeUnterminatedString, "unterminated interpolated string literal", "close the string literal with \"")
			} else if kind == token.Bytes {
				l.errorAt(start, CodeUnterminatedString, "unterminated byte string literal", "close the string literal with \"")
			} else {
				l.errorAt(start, CodeUnterminatedString, "unterminated string literal", "close the string literal with \"")
			}
			break
		}
		c := l.src[l.off]
		if c == '"' {
			rawEnd := l.off
			l.advance()
			l.emit(kind, start, startOff)
			if kind == token.FString {
				l.setValue(string(l.src[rawStart:rawEnd]))
			} else {
				l.setValue(string(value))
			}
			return
		}
		if c == '\\' {
			if kind == token.FString {
				l.advance()
				if l.off < len(l.src) && l.src[l.off] != '\n' {
					l.advance()
				}
				continue
			}
			value = append(value, l.scanEscape(kind == token.Bytes)...)
			continue
		}
		r, size := utf8.DecodeRune(l.src[l.off:])
		if r == utf8.RuneError && size == 1 {
			l.errorAt(l.pos(), CodeInvalidChar, "invalid UTF-8 in string literal", "write ASCII text or a \\u escape")
		}
		value = append(value, l.src[l.off:l.off+size]...)
		for i := 0; i < size; i++ {
			l.advance()
		}
	}
	l.emit(kind, start, startOff)
	if kind == token.FString {
		l.setValue(string(l.src[rawStart:l.off]))
	} else {
		l.setValue(string(value))
	}
}

func (l *Lexer) setValue(value string) {
	l.toks[len(l.toks)-1].Value = value
}

func (l *Lexer) scanEscape(byteString bool) []byte {
	escPos := l.pos()
	l.advance()
	if l.off >= len(l.src) {
		l.errorAt(escPos, CodeBadEscape, "unterminated escape sequence", "write an escape character after the backslash")
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
		return l.scanHexEscape()
	case 'u':
		l.advance()
		return l.scanUnicodeEscape(byteString)
	default:
		r, size := utf8.DecodeRune(l.src[l.off:])
		if r == utf8.RuneError && size == 1 {
			l.advance()
			l.errorAt(escPos, CodeBadEscape, "unknown escape sequence: \\?", "write a defined escape sequence")
			return nil
		}
		for i := 0; i < size; i++ {
			l.advance()
		}
		l.errorAt(escPos, CodeBadEscape, fmt.Sprintf("unknown escape sequence: \\%c", r), "write \\\\, \\\", \\', \\n, \\r, \\t, \\0, or \\xNN")
		return nil
	}
}

func (l *Lexer) scanHexEscape() []byte {
	pos := l.pos()
	var value byte
	for i := 0; i < 2; i++ {
		v := digitValue(l.peek(0))
		if v < 0 || v >= 16 {
			l.errorAt(pos, CodeBadHexEscape, "hex escape must have exactly two hexadecimal digits", "write two hexadecimal digits after \\x")
			return nil
		}
		value = value*16 + byte(v)
		l.advance()
	}
	return []byte{value}
}

func (l *Lexer) scanUnicodeEscape(byteString bool) []byte {
	pos := l.pos()
	if byteString {
		l.errorAt(pos, CodeUnicodeInBytes, "unicode escape is not allowed in a byte string", "use \\xNN for byte escapes")
		l.skipUnicodeEscape()
		return nil
	}
	var digits int
	var value rune
	if l.peek(0) == '{' {
		l.advance()
		for {
			c := l.peek(0)
			if c == '}' {
				l.advance()
				break
			}
			v := digitValue(c)
			if v < 0 || v >= 16 {
				l.errorAt(pos, CodeBadUnicodeEscape, "unicode escape must be 1 to 6 hexadecimal digits between braces", "write 1 to 6 hexadecimal digits between { and }")
				l.skipUnicodeEscape()
				return nil
			}
			digits++
			if digits > 6 {
				l.errorAt(pos, CodeBadUnicodeEscape, "unicode escape must be 1 to 6 hexadecimal digits between braces", "write 1 to 6 hexadecimal digits between { and }")
				l.skipUnicodeEscape()
				return nil
			}
			value = value*16 + rune(v)
			l.advance()
		}
		if digits == 0 {
			l.errorAt(pos, CodeBadUnicodeEscape, "unicode escape must be 1 to 6 hexadecimal digits between braces", "write 1 to 6 hexadecimal digits between { and }")
			return nil
		}
	} else {
		for i := 0; i < 4; i++ {
			v := digitValue(l.peek(0))
			if v < 0 || v >= 16 {
				l.errorAt(pos, CodeBadUnicodeEscape, "unicode escape must have exactly four hexadecimal digits", "write four hexadecimal digits after \\u")
				return nil
			}
			value = value*16 + rune(v)
			l.advance()
		}
	}
	if !utf8.ValidRune(value) {
		l.errorAt(pos, CodeBadUnicodeEscape, "unicode escape is not a valid code point", "write a code point in the range U+0000..U+10FFFF")
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
