package lexer

import (
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/token"
)

func (l *Lexer) scanLineComment() {
	startOff := l.off
	start := l.pos()
	doc := l.peek(2) == '/'
	l.advance()
	l.advance()
	if doc {
		l.advance()
	}
	contentOff := l.off
	for l.off < len(l.src) && l.src[l.off] != '\n' {
		l.advance()
	}
	kind := token.LineComment
	if doc {
		kind = token.DocComment
	}
	l.toks = append(l.toks, token.Token{
		Kind:  kind,
		Text:  string(l.src[startOff:l.off]),
		Value: string(l.src[contentOff:l.off]),
		Pos:   start,
		End:   l.pos(),
	})
}

func (l *Lexer) scanBlockComment() {
	startOff := l.off
	start := l.pos()
	l.advance()
	l.advance()
	doc := l.peek(0) == '*' && l.peek(1) != '/'
	contentOff := l.off
	if doc {
		l.advance()
		contentOff = l.off
	}
	for l.off < len(l.src) {
		if l.src[l.off] == '*' && l.peek(1) == '/' {
			endOff := l.off
			kind := token.BlockComment
			if doc {
				kind = token.DocComment
			}
			l.advance()
			l.advance()
			l.toks = append(l.toks, token.Token{
				Kind:  kind,
				Text:  string(l.src[startOff:l.off]),
				Value: string(l.src[contentOff:endOff]),
				Pos:   start,
				End:   l.pos(),
			})
			return
		}
		l.advance()
	}
	l.report(start, diag.UnterminatedBlockComment)
}
