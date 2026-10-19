// Package fmt renders will source in the canonical layout of the tools
// volume: four-space indents, one statement per line, the left brace at the
// end of its header line, canonical operator spacing, at most one blank line,
// and exactly one final newline.
package fmt

import (
	"strings"

	"github.com/will-lang/will/internal/lexer"
	"github.com/will-lang/will/internal/token"
)

// Source returns the canonical text of src. It is idempotent and never drops
// a token.
func Source(src []byte) []byte {
	toks, ds := lexer.ScanAll("", src)
	if len(ds) > 0 {
		return src
	}
	lines := splitLines(toks)
	lines = mergeBraces(lines)
	lines = mergeEmptyBlocks(lines)
	return render(lines)
}

// splitLines groups tokens into source lines at newline tokens.
func splitLines(toks []token.Token) [][]token.Token {
	var lines [][]token.Token
	cur := []token.Token{}
	for _, t := range toks {
		switch t.Kind {
		case token.EOF:
			if len(cur) > 0 {
				lines = append(lines, cur)
			}
			return lines
		case token.Newline:
			lines = append(lines, cur)
			cur = []token.Token{}
		default:
			cur = append(cur, t)
		}
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	return lines
}

// mergeBraces joins a lone left brace onto the previous line, which is the
// rule for function, control-flow, and type bodies.
func mergeBraces(lines [][]token.Token) [][]token.Token {
	out := make([][]token.Token, 0, len(lines))
	for _, line := range lines {
		if len(line) == 1 && line[0].Kind == token.LBrace && len(out) > 0 {
			prev := out[len(out)-1]
			if len(prev) > 0 && !endsOpen(prev[len(prev)-1].Kind) && !isComment(prev[len(prev)-1].Kind) {
				out[len(out)-1] = append(prev, line[0])
				continue
			}
		}
		out = append(out, line)
	}
	return out
}

// mergeEmptyBlocks writes an empty body as {} on its header line, which is
// the rule the tools volume gives for an empty block.
func mergeEmptyBlocks(lines [][]token.Token) [][]token.Token {
	out := make([][]token.Token, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if len(line) == 0 || line[len(line)-1].Kind != token.LBrace {
			out = append(out, line)
			continue
		}
		j := i + 1
		for j < len(lines) && len(lines[j]) == 0 {
			j++
		}
		if j < len(lines) && len(lines[j]) == 1 && lines[j][0].Kind == token.RBrace {
			merged := append(append([]token.Token{}, line...), lines[j][0])
			out = append(out, merged)
			i = j
			continue
		}
		out = append(out, line)
	}
	return out
}

func endsOpen(k token.Kind) bool {
	switch k {
	case token.LParen, token.LBracket, token.Comma, token.Colon, token.Assign,
		token.Arrow, token.FatArrow, token.Dot:
		return true
	}
	return isBinary(k)
}

func render(lines [][]token.Token) []byte {
	var out strings.Builder
	depth := 0
	paren := 0
	prevBlank := false
	for _, line := range lines {
		if len(line) == 0 {
			if depth == 0 && out.Len() == 0 {
				continue
			}
			if prevBlank {
				continue
			}
			prevBlank = true
			out.WriteByte('\n')
			continue
		}
		prevBlank = false
		leadClose := 0
		for leadClose < len(line) && line[leadClose].Kind == token.RBrace {
			leadClose++
		}
		indent := depth - leadClose
		if indent < 0 {
			indent = 0
		}
		extra := 0
		if paren > 0 && leadClose == 0 {
			extra = 1
		}
		out.WriteString(strings.Repeat("    ", indent+extra))
		writeLine(&out, line)
		out.WriteByte('\n')
		for _, t := range line {
			switch t.Kind {
			case token.LBrace:
				depth++
			case token.RBrace:
				depth--
			case token.LParen, token.LBracket:
				paren++
			case token.RParen, token.RBracket:
				paren--
			}
		}
	}
	text := strings.TrimRight(out.String(), "\n")
	if text == "" {
		return nil
	}
	return []byte(text + "\n")
}

func writeLine(out *strings.Builder, line []token.Token) {
	var prev *token.Token
	prevUnary := false
	for i := range line {
		t := line[i]
		unary := isUnary(t.Kind, prev, prevUnary)
		if prev != nil {
			gap := t.Pos.Offset > prev.End.Offset
			if separate(*prev, t, prevUnary, unary, gap) {
				out.WriteByte(' ')
			}
		}
		out.WriteString(t.Text)
		prevUnary = unary
		prev = &line[i]
	}
}

// ambiguousKinds are tokens whose canonical spacing is not yet fixed by the
// tools volume (type arguments versus comparison, ranges, postfix). Their
// original adjacency is preserved.
func ambiguous(k token.Kind) bool {
	switch k {
	case token.Lt, token.Gt, token.Shl, token.Shr, token.ShlWrap,
		token.Dot, token.DotDotDot, token.Question, token.Bang:
		return true
	}
	return false
}

// isUnary reports whether op is a prefix operator here.
func isUnary(k token.Kind, prev *token.Token, prevUnary bool) bool {
	switch k {
	case token.Minus, token.Bang, token.Tilde:
	default:
		return false
	}
	if prev == nil {
		return true
	}
	if prevUnary && prev.Kind != token.RParen && prev.Kind != token.RBracket {
		return true
	}
	switch prev.Kind {
	case token.LParen, token.LBracket, token.LBrace, token.Comma, token.Colon,
		token.Assign, token.Arrow, token.FatArrow, token.Question, token.Dot,
		token.KwReturn, token.KwIf, token.KwMatch, token.KwFor, token.KwSpawn,
		token.KwDeadline, token.KwIn:
		return true
	}
	return isBinary(prev.Kind) || isAssign(prev.Kind)
}

func isBinary(k token.Kind) bool {
	switch k {
	case token.Plus, token.Minus, token.Star, token.Slash, token.Percent,
		token.PlusWrap, token.MinusWrap, token.StarWrap,
		token.PlusSat, token.MinusSat, token.StarSat,
		token.Shl, token.Shr, token.ShlWrap,
		token.Amp, token.Pipe, token.Caret,
		token.AndAnd, token.OrOr,
		token.Eq, token.Ne, token.Lt, token.Le, token.Gt, token.Ge:
		return true
	}
	return false
}

func isAssign(k token.Kind) bool {
	switch k {
	case token.Assign, token.AddAssign, token.SubAssign, token.MulAssign,
		token.DivAssign, token.ModAssign, token.AndAssign, token.OrAssign,
		token.XorAssign, token.ShlAssign, token.ShrAssign,
		token.PlusWrapAssign, token.MinusWrapAssign, token.StarWrapAssign,
		token.PlusSatAssign, token.MinusSatAssign, token.StarSatAssign,
		token.ShlWrapAssign:
		return true
	}
	return false
}

func isComment(k token.Kind) bool { return k.IsComment() }

// separate reports whether a space is written between prev and cur. When
// either token has spacing the tools volume does not fix yet, the original
// gap is kept.
func separate(prev, cur token.Token, prevUnary, curUnary, origGap bool) bool {
	if ambiguous(prev.Kind) || ambiguous(cur.Kind) {
		return origGap
	}
	if prevUnary && !isComment(cur.Kind) {
		return false
	}
	if curUnary {
		switch prev.Kind {
		case token.LParen, token.LBracket, token.Dot:
			return false
		}
		return true
	}
	switch prev.Kind {
	case token.Dot, token.DotDotDot, token.LParen, token.LBracket:
		return false
	case token.RBracket:
		if cur.Kind == token.Ident || cur.Kind == token.LBracket {
			return false
		}
		return true
	case token.Comma, token.Colon:
		return cur.Kind != token.RBrace
	}
	switch cur.Kind {
	case token.Comma, token.Colon, token.RParen, token.RBracket, token.RBrace:
		return false
	case token.Dot, token.DotDotDot, token.Question:
		return false
	case token.LParen:
		switch prev.Kind {
		case token.Ident, token.RParen, token.RBracket, token.Gt:
			return false
		}
		return true
	case token.LBracket:
		switch prev.Kind {
		case token.Ident, token.RParen, token.RBracket:
			return false
		}
		return true
	case token.LBrace:
		return true
	}
	return true
}
