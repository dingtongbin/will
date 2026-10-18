// Package lexer turns will source bytes into tokens and lexical diagnostics.
package lexer

import "github.com/will-lang/will/internal/token"

func (l *Lexer) scanNumber() {
	startOff := l.off
	start := l.pos()
	if l.peek(0) == '0' {
		switch l.peek(1) {
		case 'x', 'X':
			l.advance()
			l.advance()
			l.scanBaseDigits(16)
			l.scanSuffix()
			l.emit(token.Int, start, startOff)
			return
		case 'b', 'B':
			l.advance()
			l.advance()
			l.scanBaseDigits(2)
			l.scanSuffix()
			l.emit(token.Int, start, startOff)
			return
		case 'o', 'O':
			l.advance()
			l.advance()
			l.scanBaseDigits(8)
			l.scanSuffix()
			l.emit(token.Int, start, startOff)
			return
		}
	}
	l.scanDecimalDigits()
	isFloat := false
	if l.off < len(l.src) && l.src[l.off] == '.' && isASCIIDigit(l.peek(1)) {
		isFloat = true
		l.advance()
		l.scanDecimalDigits()
	}
	if c := l.peek(0); c == 'e' || c == 'E' {
		isFloat = true
		expPos := l.pos()
		l.advance()
		if c := l.peek(0); c == '+' || c == '-' {
			l.advance()
		}
		if !isASCIIDigit(l.peek(0)) {
			l.errorAt(expPos, CodeBadNumber, "float exponent has no digits", "write at least one digit in the exponent")
		} else {
			l.scanDecimalDigits()
		}
	}
	l.scanSuffix()
	if isFloat {
		l.emit(token.Float, start, startOff)
		return
	}
	l.emit(token.Int, start, startOff)
}

func (l *Lexer) scanBaseDigits(base int) {
	digits := 0
	lastUnderscore := false
	for l.off < len(l.src) {
		c := l.src[l.off]
		if c == '_' {
			next := digitValue(l.peek(1))
			if digits == 0 || lastUnderscore || next < 0 || next >= base {
				l.errorAt(l.pos(), CodeBadNumber, "invalid digit separator in numeric literal", "write underscores only between digits")
			}
			lastUnderscore = true
			l.advance()
			continue
		}
		if v := digitValue(c); v >= 0 && v < base {
			digits++
			lastUnderscore = false
			l.advance()
			continue
		}
		if isASCIIDigit(c) {
			l.errorAt(l.pos(), CodeBadNumber, "digit is not valid for this numeric base", "use only digits valid for this base")
			l.advance()
			continue
		}
		break
	}
	if digits == 0 {
		l.errorAt(l.pos(), CodeBadNumber, "numeric literal has no digits after the base prefix", "write at least one digit after the base prefix")
	}
	if lastUnderscore {
		l.errorAt(l.pos(), CodeBadNumber, "numeric literal ends with a digit separator", "remove the trailing underscore")
	}
}

func (l *Lexer) scanDecimalDigits() {
	digits := 0
	lastUnderscore := false
	for l.off < len(l.src) {
		c := l.src[l.off]
		if c == '_' {
			if digits == 0 || lastUnderscore || !isASCIIDigit(l.peek(1)) {
				l.errorAt(l.pos(), CodeBadNumber, "invalid digit separator in numeric literal", "write underscores only between digits")
			}
			lastUnderscore = true
			l.advance()
			continue
		}
		if isASCIIDigit(c) {
			digits++
			lastUnderscore = false
			l.advance()
			continue
		}
		break
	}
	if lastUnderscore {
		l.errorAt(l.pos(), CodeBadNumber, "numeric literal ends with a digit separator", "remove the trailing underscore")
	}
}

func (l *Lexer) scanSuffix() {
	for l.off < len(l.src) && isIdentContinue(l.src[l.off]) {
		l.advance()
	}
}
