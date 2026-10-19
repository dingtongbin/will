package lexer

import (
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

// digitScan records one run of digits and separators.
type digitScan struct {
	digits   int
	ok       bool
	firstSep source.Pos
	hasSep   bool
}

func (l *Lexer) scanNumber() {
	startOff := l.off
	start := l.pos()

	if l.peek(0) == '0' && isBasePrefix(l.peek(1)) {
		l.scanBasedNumber(start, startOff)
		return
	}

	d := l.scanDecimalDigits()
	valid := d.ok
	isFloat := false
	var firstSep source.Pos
	hasSep := false
	noteSep(&firstSep, &hasSep, d)

	if l.peek(0) == '.' && l.peek(1) != '.' {
		if isASCIIDigit(l.peek(1)) {
			isFloat = true
			l.advance()
			d = l.scanDecimalDigits()
			valid = valid && d.ok
			noteSep(&firstSep, &hasSep, d)
		} else {
			valid = false
			l.report(l.pos(), diag.InvalidNumber)
			l.advance()
		}
	}

	if c := l.peek(0); c == 'e' || c == 'E' {
		isFloat = true
		expPos := l.pos()
		l.advance()
		if c := l.peek(0); c == '+' || c == '-' {
			l.advance()
		}
		if !isASCIIDigit(l.peek(0)) {
			valid = false
			l.report(expPos, diag.InvalidNumber)
		} else {
			d = l.scanDecimalDigits()
			valid = valid && d.ok
			noteSep(&firstSep, &hasSep, d)
		}
	}

	if isFloat && hasSep {
		l.report(firstSep, diag.InvalidNumber)
		valid = false
	}

	suffixPos := l.pos()
	suffix := l.scanSuffix()
	kind := token.Int
	if isFloat {
		kind = token.Float
	}
	if suffix != "" {
		suffixKind, ok := classifySuffix(suffix, isFloat)
		if !ok {
			l.report(suffixPos, diag.InvalidNumber)
			valid = false
		} else {
			kind = suffixKind
		}
	}
	if !valid {
		kind = token.Invalid
	}
	l.emit(kind, start, startOff)
}

func (l *Lexer) scanBasedNumber(start source.Pos, startOff int) {
	base := 0
	switch l.peek(1) {
	case 'x':
		base = 16
	case 'b':
		base = 2
	case 'o':
		base = 8
	}
	l.advance()
	l.advance()

	d := l.scanBaseDigits(base)
	valid := d.ok
	if d.digits == 0 {
		if d.ok {
			l.report(l.pos(), diag.InvalidNumber)
		}
		valid = false
	}

	suffixPos := l.pos()
	suffix := l.scanSuffix()
	kind := token.Int
	if suffix != "" {
		suffixKind, ok := classifySuffix(suffix, false)
		if !ok || suffixKind != token.Int && suffixKind != token.Duration {
			l.report(suffixPos, diag.InvalidNumber)
			valid = false
		} else {
			kind = suffixKind
		}
	}
	if !valid {
		kind = token.Invalid
	}
	l.emit(kind, start, startOff)
}

func (l *Lexer) scanDecimalDigits() digitScan {
	d := digitScan{ok: true}
	lastUnderscore := false
	for {
		c := l.peek(0)
		switch {
		case c == '_':
			if d.digits == 0 || lastUnderscore || !isASCIIDigit(l.peek(1)) {
				l.report(l.pos(), diag.InvalidNumber)
				d.ok = false
			} else if !d.hasSep {
				d.firstSep = l.pos()
				d.hasSep = true
			}
			lastUnderscore = true
			l.advance()
		case isASCIIDigit(c):
			d.digits++
			lastUnderscore = false
			l.advance()
		default:
			return d
		}
	}
}

func (l *Lexer) scanBaseDigits(base int) digitScan {
	d := digitScan{ok: true}
	lastUnderscore := false
	for {
		c := l.peek(0)
		switch {
		case c == '_':
			next := digitValue(l.peek(1))
			if d.digits == 0 || lastUnderscore || next < 0 || next >= base {
				l.report(l.pos(), diag.InvalidNumber)
				d.ok = false
			} else if !d.hasSep {
				d.firstSep = l.pos()
				d.hasSep = true
			}
			lastUnderscore = true
			l.advance()
		case digitValue(c) >= 0 && digitValue(c) < base:
			d.digits++
			lastUnderscore = false
			l.advance()
		case isASCIIDigit(c):
			l.report(l.pos(), diag.InvalidNumber)
			d.ok = false
			lastUnderscore = false
			l.advance()
		default:
			return d
		}
	}
}

func (l *Lexer) scanSuffix() string {
	start := l.off
	for l.off < len(l.src) && isIdentContinue(l.src[l.off]) {
		l.advance()
	}
	return string(l.src[start:l.off])
}

func noteSep(first *source.Pos, has *bool, d digitScan) {
	if d.hasSep && !*has {
		*first = d.firstSep
		*has = true
	}
}

func isBasePrefix(b byte) bool {
	return b == 'x' || b == 'b' || b == 'o'
}

// classifySuffix maps a literal suffix to its token kind. An empty suffix
// keeps the kind implied by the number's shape.
func classifySuffix(s string, isFloat bool) (token.Kind, bool) {
	if s == "" {
		if isFloat {
			return token.Float, true
		}
		return token.Int, true
	}
	switch s {
	case "i8", "i16", "i32", "i64", "isize",
		"u8", "u16", "u32", "u64", "usize":
		if isFloat {
			return token.Invalid, false
		}
		return token.Int, true
	case "f32", "f64":
		return token.Float, true
	case "ns", "us", "ms", "s", "m", "h":
		if isFloat {
			return token.Invalid, false
		}
		return token.Duration, true
	}
	return token.Invalid, false
}
