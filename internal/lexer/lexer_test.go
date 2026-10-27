package lexer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/token"
)

type wantToken struct {
	kind  token.Kind
	text  string
	value string
}

func scan(t *testing.T, src string) ([]token.Token, []diag.Diagnostic) {
	t.Helper()
	return Scan("test.will", []byte(src))
}

func want(kind token.Kind, text, value string) wantToken {
	return wantToken{kind: kind, text: text, value: value}
}

func checkTokens(t *testing.T, src string, expected []wantToken) {
	t.Helper()
	if !strings.HasSuffix(src, "\n") {
		src += "\n"
		n := len(expected)
		if n > 0 && expected[n-1].kind == token.EOF {
			expected = append(expected[:n-1], want(token.Newline, "\n", ""), expected[n-1])
		}
	}
	toks, ds := scan(t, src)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
	}
	if len(toks) != len(expected) {
		t.Fatalf("token count = %d, want %d\n%s", len(toks), len(expected), dump(toks))
	}
	for i := range expected {
		g, w := toks[i], expected[i]
		if g.Kind != w.kind || g.Text != w.text || g.Value != w.value {
			t.Errorf("token %d = (%s, %q, %q), want (%s, %q, %q)",
				i, g.Kind, g.Text, g.Value, w.kind, w.text, w.value)
		}
	}
}

func dump(toks []token.Token) string {
	var b strings.Builder
	for _, tk := range toks {
		fmt.Fprintf(&b, "  %s %q %q %d:%d\n", tk.Kind, tk.Text, tk.Value, tk.Pos.Line, tk.Pos.Col)
	}
	return b.String()
}

func TestOperatorsAndIdentifiers(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []wantToken
	}{
		{
			name: "minus is never part of a name",
			src:  "a-b",
			want: []wantToken{
				want(token.Ident, "a", "a"),
				want(token.Minus, "-", ""),
				want(token.Ident, "b", "b"),
				want(token.EOF, "", ""),
			},
		},
		{
			name: "variadic expansion",
			src:  "xs...",
			want: []wantToken{
				want(token.Ident, "xs", "xs"),
				want(token.DotDotDot, "...", ""),
				want(token.EOF, "", ""),
			},
		},
		{
			name: "arithmetic modes",
			src:  "a+%b -% *% +| -| *| <<% >>",
			want: []wantToken{
				want(token.Ident, "a", "a"),
				want(token.PlusWrap, "+%", ""),
				want(token.Ident, "b", "b"),
				want(token.MinusWrap, "-%", ""),
				want(token.StarWrap, "*%", ""),
				want(token.PlusSat, "+|", ""),
				want(token.MinusSat, "-|", ""),
				want(token.StarSat, "*|", ""),
				want(token.ShlWrap, "<<%", ""),
				want(token.Shr, ">>", ""),
				want(token.EOF, "", ""),
			},
		},
		{
			name: "assignments",
			src:  "= += -= *= /= %= &= |= ^= <<= >>= +%= -%= *%= +|= -|= *|= <<%=",
			want: []wantToken{
				want(token.Assign, "=", ""),
				want(token.AddAssign, "+=", ""),
				want(token.SubAssign, "-=", ""),
				want(token.MulAssign, "*=", ""),
				want(token.DivAssign, "/=", ""),
				want(token.ModAssign, "%=", ""),
				want(token.AndAssign, "&=", ""),
				want(token.OrAssign, "|=", ""),
				want(token.XorAssign, "^=", ""),
				want(token.ShlAssign, "<<=", ""),
				want(token.ShrAssign, ">>=", ""),
				want(token.PlusWrapAssign, "+%=", ""),
				want(token.MinusWrapAssign, "-%=", ""),
				want(token.StarWrapAssign, "*%=", ""),
				want(token.PlusSatAssign, "+|=", ""),
				want(token.MinusSatAssign, "-|=", ""),
				want(token.StarSatAssign, "*|=", ""),
				want(token.ShlWrapAssign, "<<%=", ""),
				want(token.EOF, "", ""),
			},
		},
		{
			name: "comments and documentation comments",
			src:  "# plain\n## one\n\"\"\" block \"\"\"\n\"\"\" two \"\"\"\nx",
			want: []wantToken{
				want(token.Newline, "\n", ""),
				want(token.DocComment, "## one", " one"),
				want(token.Newline, "\n", ""),
				want(token.Newline, "\n", ""),
				want(token.Newline, "\n", ""),
				want(token.Ident, "x", "x"),
				want(token.EOF, "", ""),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkTokens(t, c.src, c.want)
		})
	}
}

func TestStrictKeywords(t *testing.T) {
	src := "def func type async await struct enum interface const var import derive lane " +
		"if else while for in match return break continue defer " +
		"region spawn parallel deadline select and or not True False None"
	expected := []wantToken{}
	for _, word := range strings.Fields(src) {
		kind, ok := token.Keywords[word]
		if !ok {
			t.Fatalf("%q is not a keyword", word)
		}
		expected = append(expected, want(kind, word, ""))
	}
	expected = append(expected, want(token.EOF, "", ""))
	checkTokens(t, src, expected)
}

func TestContextualAndPredeclaredNamesAreIdentifiers(t *testing.T) {
	words := []string{
		"on", "timeout", "default", "fast", "blocking", "mut", "new",
		"loop", "as", "let", "unreachable", "export",
		"print", "panic", "assert", "c", "send", "recv", "is", "init",
	}
	src := strings.Join(words, " ")
	expected := []wantToken{}
	for _, word := range words {
		expected = append(expected, want(token.Ident, word, word))
	}
	expected = append(expected, want(token.EOF, "", ""))
	checkTokens(t, src, expected)
}

func TestScanAllKeepsComments(t *testing.T) {
	toks, ds := ScanAll("test.will", []byte("# line\n\"\"\" block \"\"\" x\n"))
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
	}
	kinds := []token.Kind{}
	for _, tk := range toks {
		kinds = append(kinds, tk.Kind)
	}
	want := []token.Kind{token.LineComment, token.Newline, token.BlockComment, token.Ident, token.Newline, token.EOF}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("token %d = %s, want %s", i, kinds[i], want[i])
		}
	}
	if toks[0].Text != "# line" || toks[0].Value != " line" {
		t.Errorf("line comment = %q %q", toks[0].Text, toks[0].Value)
	}
	if toks[2].Text != "\"\"\" block \"\"\"" || toks[2].Value != " block " {
		t.Errorf("block comment = %q %q", toks[2].Text, toks[2].Value)
	}
}

func TestPositions(t *testing.T) {
	src := "\"\"\"\n\"\"\"x\ny\n"
	toks, ds := scan(t, src)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
	}
	idents := []token.Token{}
	for _, tk := range toks {
		if tk.Kind == token.Ident {
			idents = append(idents, tk)
		}
	}
	if len(idents) != 2 {
		t.Fatalf("identifiers = %d, want 2", len(idents))
	}
	if idents[0].Text != "x" || idents[0].Pos.Line != 2 || idents[0].Pos.Col != 4 {
		t.Errorf("x is at %d:%d, want 2:4", idents[0].Pos.Line, idents[0].Pos.Col)
	}
	if idents[1].Text != "y" || idents[1].Pos.Line != 3 || idents[1].Pos.Col != 1 {
		t.Errorf("y is at %d:%d, want 3:1", idents[1].Pos.Line, idents[1].Pos.Col)
	}
}

func TestForbiddenSymbols(t *testing.T) {
	cases := []struct {
		src string
		col int
	}{
		{";", 1},
		{"@", 1},
		{"$", 1},
		{"`", 1},
		{"a::b", 2},
		{"a..b", 2},
		{"\\", 1},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			toks, ds := scan(t, c.src+"\n")
			d := findDiagnostic(t, ds, diag.CodeBadSymbol, diag.BadSymbol.Text)
			if d.Pos.Line != 1 || d.Pos.Col != c.col {
				t.Errorf("position = %d:%d, want 1:%d", d.Pos.Line, d.Pos.Col, c.col)
			}
			if d.Help != diag.BadSymbol.Help {
				t.Errorf("help = %q, want %q", d.Help, diag.BadSymbol.Help)
			}
			found := false
			for _, tk := range toks {
				if tk.Kind == token.Invalid {
					found = true
				}
			}
			if !found {
				t.Errorf("no invalid token in %s", dump(toks))
			}
		})
	}
}

func findDiagnostic(t *testing.T, ds []diag.Diagnostic, code, message string) diag.Diagnostic {
	t.Helper()
	for _, d := range ds {
		if d.Code == code && d.Message == message {
			return d
		}
	}
	t.Fatalf("missing diagnostic %s %q\ngot:\n%s", code, message, diag.FormatAll(ds))
	return diag.Diagnostic{}
}
