// Package lexer turns will source bytes into tokens and lexical diagnostics.
package lexer

import (
	"unicode/utf8"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

// Scan tokenizes src and returns the token stream followed by EOF.
//
// Byte-level rules are checked for the whole file before scanning, so a
// malformed byte inside a comment or literal is still reported with a
// position.
func Scan(file string, src []byte) ([]token.Token, []diag.Diagnostic) {
	l := &Lexer{
		file: file,
		src:  src,
		line: 1,
		col:  1,
	}
	if l.checkFile() {
		l.run()
	}
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

func (l *Lexer) report(pos source.Pos, m diag.Message) {
	l.bag.Report(pos, m)
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
	b := l.src[l.off]

	if b == '.' {
		switch {
		case l.peek(1) == '.' && l.peek(2) == '.':
			l.advance()
			l.advance()
			l.advance()
			l.emit(token.DotDotDot, start, startOff)
			return
		case l.peek(1) == '.':
			l.advance()
			l.advance()
			l.emit(token.DotDot, start, startOff)
			return
		case isASCIIDigit(l.peek(1)):
			l.report(start, diag.InvalidNumber)
			l.advance()
			for isASCIIDigit(l.peek(0)) {
				l.advance()
			}
			l.emit(token.Invalid, start, startOff)
			return
		}
	}
	if b == ':' && l.peek(1) == ':' {
		l.report(start, diag.BadSymbol)
		l.advance()
		l.advance()
		l.emit(token.Invalid, start, startOff)
		return
	}
	if b == ';' {
		l.report(start, diag.BadSymbol)
		l.advance()
		l.emit(token.Invalid, start, startOff)
		return
	}
	if kind, n := l.matchFixed(); n > 0 {
		for i := 0; i < n; i++ {
			l.advance()
		}
		l.emit(kind, start, startOff)
		return
	}
	if kind, ok := oneChar[b]; ok {
		l.advance()
		l.emit(kind, start, startOff)
		return
	}

	// Byte-level checks already reported non-ASCII and control bytes; skip
	// them here so one bad byte does not produce a second diagnostic.
	if b < 0x80 {
		if b >= 0x20 && b <= 0x7E {
			l.report(start, diag.BadSymbol)
		}
		l.advance()
		l.emit(token.Invalid, start, startOff)
		return
	}
	_, size := utf8.DecodeRune(l.src[l.off:])
	for i := 0; i < size; i++ {
		l.advance()
	}
	l.emit(token.Invalid, start, startOff)
}

// matchFixed matches operators with a fixed spelling of two or more bytes.
// Longer spellings are tried first so that <<%= never scans as <<%.
func (l *Lexer) matchFixed() (token.Kind, int) {
	rest := l.src[l.off:]
	for _, op := range fixedOps {
		if len(rest) >= len(op.text) && string(rest[:len(op.text)]) == op.text {
			return op.kind, len(op.text)
		}
	}
	return token.Invalid, 0
}

type fixedOp struct {
	text string
	kind token.Kind
}

var fixedOps = []fixedOp{
	{"<<%=", token.ShlWrapAssign},
	{"+%=", token.PlusWrapAssign},
	{"-%=", token.MinusWrapAssign},
	{"*%=", token.StarWrapAssign},
	{"+|=", token.PlusSatAssign},
	{"-|=", token.MinusSatAssign},
	{"*|=", token.StarSatAssign},
	{"<<=", token.ShlAssign},
	{">>=", token.ShrAssign},
	{"<<%", token.ShlWrap},
	{"+%", token.PlusWrap},
	{"-%", token.MinusWrap},
	{"*%", token.StarWrap},
	{"+|", token.PlusSat},
	{"-|", token.MinusSat},
	{"*|", token.StarSat},
	{"<<", token.Shl},
	{">>", token.Shr},
	{"&&", token.AndAnd},
	{"||", token.OrOr},
	{"==", token.Eq},
	{"!=", token.Ne},
	{"<=", token.Le},
	{">=", token.Ge},
	{"->", token.Arrow},
	{"=>", token.FatArrow},
	{"+=", token.AddAssign},
	{"-=", token.SubAssign},
	{"*=", token.MulAssign},
	{"/=", token.DivAssign},
	{"%=", token.ModAssign},
	{"&=", token.AndAssign},
	{"|=", token.OrAssign},
	{"^=", token.XorAssign},
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
