// Package lexer turns will source bytes into tokens and lexical diagnostics.
package lexer

import (
	"fmt"
	"unicode/utf8"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

// Lexical diagnostic codes are stable once published.
const (
	// CodeInvalidChar reports a byte that cannot start any token.
	CodeInvalidChar = "E1001"
	// CodeUnterminatedComment reports a block comment without a closing marker.
	CodeUnterminatedComment = "E1002"
	// CodeBadNumber reports a malformed numeric literal.
	CodeBadNumber = "E1003"
	// CodeUnterminatedString reports a string literal without a closing quote.
	CodeUnterminatedString = "E1004"
	// CodeUnterminatedChar reports a character literal without a closing quote.
	CodeUnterminatedChar = "E1005"
	// CodeBadChar reports a character literal that does not hold exactly one character.
	CodeBadChar = "E1006"
	// CodeBadEscape reports an escape sequence the language does not define.
	CodeBadEscape = "E1007"
	// CodeBadHexEscape reports a malformed \xNN escape.
	CodeBadHexEscape = "E1008"
	// CodeBadUnicodeEscape reports a malformed \u escape.
	CodeBadUnicodeEscape = "E1009"
	// CodeUnicodeInBytes reports a unicode escape inside a byte string.
	CodeUnicodeInBytes = "E1010"
)

// Scan tokenizes src and returns the token stream followed by EOF.
func Scan(file string, src []byte) ([]token.Token, []diag.Diagnostic) {
	l := &Lexer{
		file: file,
		src:  src,
		line: 1,
		col:  1,
	}
	l.run()
	l.toks = append(l.toks, token.Token{
		Kind: token.EOF,
		Pos:  l.pos(),
		End:  l.pos(),
	})
	return l.toks, l.bag.Diagnostics()
}

// Lexer scans one file.
type Lexer struct {
	file string
	src  []byte
	off  int
	line int
	col  int
	toks []token.Token
	bag  diag.Bag
}

func (l *Lexer) run() {
	for l.off < len(l.src) {
		b := l.src[l.off]
		switch {
		case b == ' ' || b == '\t' || b == '\r':
			l.advance()
		case b == '\n':
			start := l.pos()
			l.advance()
			l.toks = append(l.toks, token.Token{Kind: token.Newline, Text: "\n", Pos: start, End: l.pos()})
		case b == '/' && l.peek(1) == '/':
			l.scanLineComment()
		case b == '/' && l.peek(1) == '*':
			l.scanBlockComment()
		case b == 'b' && l.peek(1) == '"':
			l.scanQuoted(token.Bytes, 1)
		case b == 'f' && l.peek(1) == '"':
			l.scanQuoted(token.FString, 1)
		case isIdentStart(b):
			l.scanIdent()
		case isASCIIDigit(b):
			l.scanNumber()
		case b == '\'':
			l.scanChar()
		case b == '"':
			l.scanQuoted(token.String, 0)
		default:
			l.scanOperator()
		}
	}
}

func (l *Lexer) pos() source.Pos {
	return source.Pos{File: l.file, Offset: l.off, Line: l.line, Col: l.col}
}

func (l *Lexer) peek(n int) byte {
	if l.off+n >= len(l.src) {
		return 0
	}
	return l.src[l.off+n]
}

func (l *Lexer) advance() {
	if l.off >= len(l.src) {
		return
	}
	if l.src[l.off] == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	l.off++
}

func (l *Lexer) emit(kind token.Kind, start source.Pos, startOff int) token.Token {
	t := token.Token{
		Kind: kind,
		Text: string(l.src[startOff:l.off]),
		Pos:  start,
		End:  l.pos(),
	}
	l.toks = append(l.toks, t)
	return t
}

func (l *Lexer) errorAt(pos source.Pos, code, message, help string) {
	l.bag.Error(pos, code, message, help)
}

func (l *Lexer) scanIdent() {
	startOff := l.off
	start := l.pos()
	for l.off < len(l.src) && isIdentContinue(l.src[l.off]) {
		l.advance()
	}
	text := string(l.src[startOff:l.off])
	kind, ok := token.Keywords[text]
	value := ""
	if !ok {
		kind = token.Ident
		value = text
	}
	l.toks = append(l.toks, token.Token{Kind: kind, Text: text, Value: value, Pos: start, End: l.pos()})
}

func (l *Lexer) scanOperator() {
	startOff := l.off
	start := l.pos()
	three := ""
	if l.off+3 <= len(l.src) {
		three = string(l.src[l.off : l.off+3])
	}
	switch three {
	case "...":
		l.advance()
		l.advance()
		l.advance()
		l.emit(token.DotDotDot, start, startOff)
		return
	case "<<%":
		l.advance()
		l.advance()
		l.advance()
		l.emit(token.ShlWrap, start, startOff)
		return
	}
	two := ""
	if l.off+2 <= len(l.src) {
		two = string(l.src[l.off : l.off+2])
	}
	if kind, ok := twoChar[two]; ok {
		l.advance()
		l.advance()
		l.emit(kind, start, startOff)
		return
	}
	if kind, ok := oneChar[l.src[l.off]]; ok {
		l.advance()
		l.emit(kind, start, startOff)
		return
	}
	r, size := utf8.DecodeRune(l.src[l.off:])
	if r == utf8.RuneError && size == 1 {
		l.advance()
		l.errorAt(start, CodeInvalidChar, "unexpected byte in source", "source files must be valid UTF-8")
	} else {
		for i := 0; i < size; i++ {
			l.advance()
		}
		l.errorAt(start, CodeInvalidChar, fmt.Sprintf("unexpected character %q", r), "remove the character or write it inside a string literal")
	}
	l.emit(token.Invalid, start, startOff)
}

var twoChar = map[string]token.Kind{
	"+%": token.PlusWrap,
	"-%": token.MinusWrap,
	"*%": token.StarWrap,
	"+|": token.PlusSat,
	"-|": token.MinusSat,
	"*|": token.StarSat,
	"<<": token.Shl,
	">>": token.Shr,
	"&&": token.AndAnd,
	"||": token.OrOr,
	"==": token.Eq,
	"!=": token.Ne,
	"<=": token.Le,
	">=": token.Ge,
	"..": token.DotDot,
	"->": token.Arrow,
	"=>": token.FatArrow,
}

var oneChar = map[byte]token.Kind{
	'+': token.Plus,
	'-': token.Minus,
	'*': token.Star,
	'/': token.Slash,
	'%': token.Percent,
	'&': token.Amp,
	'|': token.Pipe,
	'^': token.Caret,
	'~': token.Tilde,
	'!': token.Bang,
	'=': token.Assign,
	'<': token.Lt,
	'>': token.Gt,
	'.': token.Dot,
	',': token.Comma,
	':': token.Colon,
	';': token.Semicolon,
	'?': token.Question,
	'(': token.LParen,
	')': token.RParen,
	'[': token.LBracket,
	']': token.RBracket,
	'{': token.LBrace,
	'}': token.RBrace,
}

func isIdentStart(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isIdentContinue(b byte) bool {
	return isIdentStart(b) || isASCIIDigit(b)
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func digitValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}
