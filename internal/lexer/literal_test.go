package lexer

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/token"
)

func TestLiterals(t *testing.T) {
	src := `'a' '\n' '\x41' '\u{1F600}' "a\tb" "\u{4e2d}" b"AB\x00" f"x {a} y"`
	checkTokens(t, src, []wantToken{
		want(token.Char, `'a'`, "a"),
		want(token.Char, `'\n'`, "\n"),
		want(token.Char, `'\x41'`, "A"),
		want(token.Char, `'\u{1F600}'`, "\U0001F600"),
		want(token.String, `"a\tb"`, "a\tb"),
		want(token.String, `"\u{4e2d}"`, "\u4e2d"),
		want(token.Bytes, `b"AB\x00"`, "AB\x00"),
		want(token.FString, `f"x {a} y"`, "x {a} y"),
		want(token.EOF, "", ""),
	})
}

func TestFStringBracesAndNestedQuotes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		body string
	}{
		{
			name: "escaped braces",
			src:  `f"a {{b}} {x} c"`,
			body: "a {{b}} {x} c",
		},
		{
			name: "quotes inside interpolation",
			src:  `f"{if ok { "yes" } else { "no" }}"`,
			body: `{if ok { "yes" } else { "no" }}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, ds := scan(t, c.src+"\n")
			if len(ds) != 0 {
				t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
			}
			if len(toks) != 3 || toks[0].Kind != token.FString || toks[0].Value != c.body {
				t.Fatalf("tokens = %s, want one f-string with body %q", dump(toks), c.body)
			}
		})
	}
}

func TestFStringCannotSpanLines(t *testing.T) {
	_, ds := scan(t, `f"{a // note`+"\n"+`}"`)
	findDiagnostic(t, ds, diag.CodeUnterminated, diag.UnterminatedString.Text)
}

func TestBadLiterals(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		code    string
		message string
		help    string
		col     int
	}{
		{"empty char", "''", diag.CodeBadChar, diag.BadChar.Text, diag.BadChar.Help, 1},
		{"many chars", "'ab'", diag.CodeBadChar, diag.BadChar.Text, diag.BadChar.Help, 1},
		{"unterminated char", "'a", diag.CodeUnterminated, diag.UnterminatedString.Text, diag.UnterminatedString.Help, 1},
		{"unterminated string", `"open`, diag.CodeUnterminated, diag.UnterminatedString.Text, diag.UnterminatedString.Help, 1},
		{"unterminated bytes", `b"open`, diag.CodeUnterminated, diag.UnterminatedString.Text, diag.UnterminatedString.Help, 1},
		{"unterminated fstring", `f"open`, diag.CodeUnterminated, diag.UnterminatedString.Text, diag.UnterminatedString.Help, 1},
		{"unknown escape", `"\q"`, diag.CodeBadEscape, diag.UnknownEscape.Text, diag.UnknownEscape.Help, 2},
		{"unknown char escape", `'\q'`, diag.CodeBadEscape, diag.UnknownEscape.Text, diag.UnknownEscape.Help, 2},
		{"short hex escape", `"\x4"`, diag.CodeBadEscape, diag.UnknownEscape.Text, diag.UnknownEscape.Help, 2},
		{"invalid code point", `"\u{110000}"`, diag.CodeBadEscape, diag.UnknownEscape.Text, diag.UnknownEscape.Help, 2},
		{"empty unicode escape", `"\u{}"`, diag.CodeBadEscape, diag.UnknownEscape.Text, diag.UnknownEscape.Help, 2},
		{"long unicode escape", `"\u{1234567}"`, diag.CodeBadEscape, diag.UnknownEscape.Text, diag.UnknownEscape.Help, 2},
		{"unicode without braces", `"\u0041"`, diag.CodeBadEscape, diag.UnknownEscape.Text, diag.UnknownEscape.Help, 2},
		{"unicode in bytes", `b"\u{41}"`, diag.CodeBadEscape, diag.UnknownEscape.Text, diag.UnknownEscape.Help, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ds := scan(t, c.src+"\n")
			d := findDiagnostic(t, ds, c.code, c.message)
			if d.Help != c.help {
				t.Errorf("help = %q, want %q", d.Help, c.help)
			}
			if d.Pos.Line != 1 || d.Pos.Col != c.col {
				t.Errorf("position = %d:%d, want 1:%d", d.Pos.Line, d.Pos.Col, c.col)
			}
		})
	}
}
