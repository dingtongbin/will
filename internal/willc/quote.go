package willc

import (
	"strings"
	"unicode/utf8"
)

// Quote writes s as a willc quoted text value. Non-printable and non-ASCII
// bytes become \xNN so the document stays ASCII.
func Quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case 0:
			b.WriteString(`\0`)
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			if c >= 0x20 && c <= 0x7E {
				b.WriteByte(c)
			} else {
				b.WriteString(`\x`)
				b.WriteByte(hexDigit(c >> 4))
				b.WriteByte(hexDigit(c & 0xF))
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Unquote reads one willc quoted text value. It accepts the escape set of
// will source strings and fails on any other form.
func Unquote(s string) (string, bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	body := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			if c < 0x20 || c > 0x7E {
				return "", false
			}
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(body) {
			return "", false
		}
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '0':
			b.WriteByte(0)
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		case '\'':
			b.WriteByte('\'')
		case 'x':
			if i+2 >= len(body) {
				return "", false
			}
			hi := hexValue(body[i+1])
			lo := hexValue(body[i+2])
			if hi < 0 || lo < 0 {
				return "", false
			}
			b.WriteByte(byte(hi*16 + lo))
			i += 2
		case 'u':
			r, next, ok := unquoteUnicode(body, i+1)
			if !ok {
				return "", false
			}
			b.WriteRune(r)
			i = next - 1
		default:
			return "", false
		}
	}
	return b.String(), true
}

func unquoteUnicode(body string, start int) (rune, int, bool) {
	if start >= len(body) || body[start] != '{' {
		return 0, 0, false
	}
	var value rune
	digits := 0
	i := start + 1
	for ; i < len(body) && body[i] != '}'; i++ {
		v := hexValue(body[i])
		if v < 0 || digits == 6 {
			return 0, 0, false
		}
		value = value*16 + rune(v)
		digits++
	}
	if i >= len(body) || digits == 0 || !utf8.ValidRune(value) {
		return 0, 0, false
	}
	return value, i + 1, true
}

func hexDigit(v byte) byte {
	if v < 10 {
		return '0' + v
	}
	return 'a' + v - 10
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
