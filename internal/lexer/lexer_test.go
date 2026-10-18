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
	toks, ds := scan(t, src)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
	}
	got := toks
	if len(got) != len(expected) {
		t.Fatalf("token count = %d, want %d\n%s", len(got), len(expected), dump(got))
	}
	for i := range expected {
		g, w := got[i], expected[i]
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
			name: "range is not a float",
			src:  "1..2",
			want: []wantToken{
				want(token.Int, "1", ""),
				want(token.DotDot, "..", ""),
				want(token.Int, "2", ""),
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
			name: "comments and documentation comments",
			src:  "// plain\n/// one\n/* block */\n/** two */\nx",
			want: []wantToken{
				want(token.Newline, "\n", ""),
				want(token.DocComment, "/// one", " one"),
				want(token.Newline, "\n", ""),
				want(token.Newline, "\n", ""),
				want(token.DocComment, "/** two */", " two "),
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

func TestNumericLiterals(t *testing.T) {
	src := "0 1_000 0xFF 0b1010 0o17 1i32 2u64 3usize 4.5 1e3 2.5f64 1E+2"
	checkTokens(t, src, []wantToken{
		want(token.Int, "0", ""),
		want(token.Int, "1_000", ""),
		want(token.Int, "0xFF", ""),
		want(token.Int, "0b1010", ""),
		want(token.Int, "0o17", ""),
		want(token.Int, "1i32", ""),
		want(token.Int, "2u64", ""),
		want(token.Int, "3usize", ""),
		want(token.Float, "4.5", ""),
		want(token.Float, "1e3", ""),
		want(token.Float, "2.5f64", ""),
		want(token.Float, "1E+2", ""),
		want(token.EOF, "", ""),
	})
}

func TestLiterals(t *testing.T) {
	src := "'a' '\\n' '\\x41' '\\u{1F600}' \"a\\tb\" b\"AB\\x00\" f\"x {a} y\""
	checkTokens(t, src, []wantToken{
		want(token.Char, "'a'", "a"),
		want(token.Char, "'\\n'", "\n"),
		want(token.Char, "'\\x41'", "A"),
		want(token.Char, "'\\u{1F600}'", "\U0001F600"),
		want(token.String, "\"a\\tb\"", "a\tb"),
		want(token.Bytes, "b\"AB\\x00\"", "AB\x00"),
		want(token.FString, "f\"x {a} y\"", "x {a} y"),
		want(token.EOF, "", ""),
	})
}

func TestKeywords(t *testing.T) {
	src := "func struct enum interface const var import derive return if else for in match " +
		"spawn region parallel deadline select panic defer new break continue true false"
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

func TestSoftKeywordsAreIdentifiers(t *testing.T) {
	checkTokens(t, "while loop as let mut unreachable export c send recv timeout default fast is init",
		[]wantToken{
			want(token.Ident, "while", "while"),
			want(token.Ident, "loop", "loop"),
			want(token.Ident, "as", "as"),
			want(token.Ident, "let", "let"),
			want(token.Ident, "mut", "mut"),
			want(token.Ident, "unreachable", "unreachable"),
			want(token.Ident, "export", "export"),
			want(token.Ident, "c", "c"),
			want(token.Ident, "send", "send"),
			want(token.Ident, "recv", "recv"),
			want(token.Ident, "timeout", "timeout"),
			want(token.Ident, "default", "default"),
			want(token.Ident, "fast", "fast"),
			want(token.Ident, "is", "is"),
			want(token.Ident, "init", "init"),
			want(token.EOF, "", ""),
		})
}

func TestPositions(t *testing.T) {
	src := "/*\n*/x\ny"
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
	if idents[0].Text != "x" || idents[0].Pos.Line != 2 || idents[0].Pos.Col != 3 {
		t.Errorf("x is at %d:%d, want 2:3", idents[0].Pos.Line, idents[0].Pos.Col)
	}
	if idents[1].Text != "y" || idents[1].Pos.Line != 3 || idents[1].Pos.Col != 1 {
		t.Errorf("y is at %d:%d, want 3:1", idents[1].Pos.Line, idents[1].Pos.Col)
	}
}

func TestDiagnostics(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		code    string
		message string
		help    string
		col     int
	}{
		{
			name:    "invalid character",
			src:     "#",
			code:    CodeInvalidChar,
			message: "unexpected character '#'",
			help:    "remove the character or write it inside a string literal",
			col:     1,
		},
		{
			name:    "unterminated block comment",
			src:     "/* open",
			code:    CodeUnterminatedComment,
			message: "unterminated block comment",
			help:    "close the comment with */",
			col:     1,
		},
		{
			name:    "base prefix without digits",
			src:     "0x",
			code:    CodeBadNumber,
			message: "numeric literal has no digits after the base prefix",
			help:    "write at least one digit after the base prefix",
			col:     3,
		},
		{
			name:    "digit outside base",
			src:     "0b102",
			code:    CodeBadNumber,
			message: "digit is not valid for this numeric base",
			help:    "use only digits valid for this base",
			col:     5,
		},
		{
			name:    "trailing separator",
			src:     "1_",
			code:    CodeBadNumber,
			message: "numeric literal ends with a digit separator",
			help:    "remove the trailing underscore",
			col:     3,
		},
		{
			name:    "separator before suffix",
			src:     "1_u8",
			code:    CodeBadNumber,
			message: "invalid digit separator in numeric literal",
			help:    "write underscores only between digits",
			col:     2,
		},
		{
			name:    "unterminated string",
			src:     "\"open",
			code:    CodeUnterminatedString,
			message: "unterminated string literal",
			help:    "close the string literal with \"",
			col:     1,
		},
		{
			name:    "unterminated byte string",
			src:     "b\"open",
			code:    CodeUnterminatedString,
			message: "unterminated byte string literal",
			help:    "close the string literal with \"",
			col:     1,
		},
		{
			name:    "unterminated character",
			src:     "'a",
			code:    CodeUnterminatedChar,
			message: "unterminated character literal",
			help:    "close the character literal with '",
			col:     1,
		},
		{
			name:    "empty character",
			src:     "''",
			code:    CodeBadChar,
			message: "empty character literal",
			help:    "write exactly one character between the quotes",
			col:     1,
		},
		{
			name:    "many characters",
			src:     "'ab'",
			code:    CodeBadChar,
			message: "character literal must contain exactly one character",
			help:    "write exactly one character between the quotes",
			col:     1,
		},
		{
			name:    "unknown escape",
			src:     "\"\\q\"",
			code:    CodeBadEscape,
			message: "unknown escape sequence: \\q",
			help:    "write \\\\, \\\", \\', \\n, \\r, \\t, \\0, or \\xNN",
			col:     2,
		},
		{
			name:    "short hex escape",
			src:     "\"\\x4\"",
			code:    CodeBadHexEscape,
			message: "hex escape must have exactly two hexadecimal digits",
			help:    "write two hexadecimal digits after \\x",
			col:     4,
		},
		{
			name:    "invalid code point",
			src:     "\"\\u{110000}\"",
			code:    CodeBadUnicodeEscape,
			message: "unicode escape is not a valid code point",
			help:    "write a code point in the range U+0000..U+10FFFF",
			col:     4,
		},
		{
			name:    "unicode escape in byte string",
			src:     "b\"\\u{41}\"",
			code:    CodeUnicodeInBytes,
			message: "unicode escape is not allowed in a byte string",
			help:    "use \\xNN for byte escapes",
			col:     5,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ds := scan(t, c.src)
			found := false
			for _, d := range ds {
				if d.Code != c.code || d.Message != c.message || d.Help != c.help {
					continue
				}
				found = true
				if d.Severity != diag.Error {
					t.Errorf("severity = %v, want error", d.Severity)
				}
				if d.Pos.Line != 1 || d.Pos.Col != c.col {
					t.Errorf("position = %d:%d, want 1:%d", d.Pos.Line, d.Pos.Col, c.col)
				}
			}
			if !found {
				t.Fatalf("missing diagnostic %s %q with help %q\ngot:\n%s", c.code, c.message, c.help, diag.FormatAll(ds))
			}
		})
	}
}

func TestDiagnosticFormat(t *testing.T) {
	_, ds := scan(t, "#")
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want 1", len(ds))
	}
	got := diag.Format(ds[0])
	want := "test.will:1:1: E1001 unexpected character '#'\n" +
		"    help: remove the character or write it inside a string literal\n"
	if got != want {
		t.Fatalf("format = %q, want %q", got, want)
	}
}
