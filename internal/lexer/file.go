package lexer

import (
	"unicode/utf8"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

var (
	utf8BOM    = []byte{0xEF, 0xBB, 0xBF}
	utf16LEBOM = []byte{0xFF, 0xFE}
	utf16BEBOM = []byte{0xFE, 0xFF}
)

// checkFile validates the byte-level rules for a source file. It leaves the
// lexer positioned after a UTF-8 BOM and reports the BOM at 1:1 so that the
// first real token still starts there.
//
// A UTF-16 file cannot be scanned as text, so checkFile reports the BOM and
// returns false; the caller then produces an empty token stream.
func (l *Lexer) checkFile() bool {
	if hasPrefix(l.src, utf16LEBOM) || hasPrefix(l.src, utf16BEBOM) {
		l.reportAt(0, 1, 1, diag.ByteOrderMark)
		return false
	}
	if hasPrefix(l.src, utf8BOM) {
		l.reportAt(0, 1, 1, diag.ByteOrderMark)
		l.off = len(utf8BOM)
	}

	off := l.off
	line, col := 1, 1
	for off < len(l.src) {
		b := l.src[off]
		switch {
		case b == '\n':
			line++
			col = 1
			off++
		case b == '\r':
			l.reportAt(off, line, col, diag.CRLineEnding)
			col++
			off++
		case b == '\t' || b >= 0x20 && b <= 0x7E:
			col++
			off++
		default:
			_, size := utf8.DecodeRune(l.src[off:])
			l.reportAt(off, line, col, diag.NonASCII)
			col += size
			off += size
		}
	}

	if len(l.src) > 0 && l.src[len(l.src)-1] != '\n' {
		l.reportAt(len(l.src), line, col, diag.MissingFinalLF)
	}
	return true
}

// checkFragment reporting: a fragment comes from a file whose byte-level rules
// were already checked, and it is not a file of its own, so no whole-file rule
// applies.
func (l *Lexer) checkFragment() bool {
	return true
}

func (l *Lexer) reportAt(off, line, col int, m diag.Message) {
	l.bag.Report(source.Pos{File: l.file, Offset: off, Line: line, Col: col}, m)
}

func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}
