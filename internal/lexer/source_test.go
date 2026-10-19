package lexer

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/token"
)

func TestUTF8BOM(t *testing.T) {
	toks, ds := scan(t, "\xEF\xBB\xBFx\n")
	d := findDiagnostic(t, ds, diag.CodeBadEncoding, diag.ByteOrderMark.Text)
	if d.Pos.Line != 1 || d.Pos.Col != 1 {
		t.Errorf("position = %d:%d, want 1:1", d.Pos.Line, d.Pos.Col)
	}
	if d.Help != diag.ByteOrderMark.Help {
		t.Errorf("help = %q, want %q", d.Help, diag.ByteOrderMark.Help)
	}
	if toks[0].Kind != token.Ident || toks[0].Text != "x" || toks[0].Pos.Col != 1 {
		t.Errorf("first token = %s %q at %d, want identifier x at 1", toks[0].Kind, toks[0].Text, toks[0].Pos.Col)
	}
}

func TestCRLineEnding(t *testing.T) {
	toks, ds := scan(t, "a\r\nb\n")
	d := findDiagnostic(t, ds, diag.CodeBadEncoding, diag.CRLineEnding.Text)
	if d.Pos.Line != 1 || d.Pos.Col != 2 {
		t.Errorf("position = %d:%d, want 1:2", d.Pos.Line, d.Pos.Col)
	}
	if len(ds) != 1 {
		t.Errorf("diagnostics = %d, want 1:\n%s", len(ds), diag.FormatAll(ds))
	}
	if toks[2].Kind != token.Ident || toks[2].Text != "b" || toks[2].Pos.Line != 2 {
		t.Errorf("token after CRLF = %s %q at line %d, want identifier b at line 2",
			toks[2].Kind, toks[2].Text, toks[2].Pos.Line)
	}
}

func TestUnterminatedBlockComment(t *testing.T) {
	_, ds := scan(t, "/* open\n")
	d := findDiagnostic(t, ds, diag.CodeUnterminated, diag.UnterminatedBlockComment.Text)
	if d.Pos.Line != 1 || d.Pos.Col != 1 {
		t.Errorf("position = %d:%d, want 1:1", d.Pos.Line, d.Pos.Col)
	}
	if d.Help != diag.UnterminatedBlockComment.Help {
		t.Errorf("help = %q, want %q", d.Help, diag.UnterminatedBlockComment.Help)
	}
}

func TestMissingFinalNewline(t *testing.T) {
	_, ds := scan(t, "a")
	d := findDiagnostic(t, ds, diag.CodeBadEncoding, diag.MissingFinalLF.Text)
	if d.Pos.Line != 1 || d.Pos.Col != 2 {
		t.Errorf("position = %d:%d, want 1:2", d.Pos.Line, d.Pos.Col)
	}
	if d.Help != diag.MissingFinalLF.Help {
		t.Errorf("help = %q, want %q", d.Help, diag.MissingFinalLF.Help)
	}
}

func TestNonASCII(t *testing.T) {
	cases := []struct {
		name string
		src  string
		col  int
	}{
		{"outside literals", "a\xC3\xA9\n", 2},
		{"inside a string", "\"\xC3\xA9\"\n", 2},
		{"NUL byte", "a\x00b\n", 2},
		{"delete byte", "a\x7Fb\n", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ds := scan(t, c.src)
			d := findDiagnostic(t, ds, diag.CodeNonASCII, diag.NonASCII.Text)
			if d.Pos.Line != 1 || d.Pos.Col != c.col {
				t.Errorf("position = %d:%d, want 1:%d", d.Pos.Line, d.Pos.Col, c.col)
			}
			if d.Help != diag.NonASCII.Help {
				t.Errorf("help = %q, want %q", d.Help, diag.NonASCII.Help)
			}
		})
	}
}

func TestUTF16BOMStopsScanning(t *testing.T) {
	toks, ds := scan(t, "\xFF\xFEx\n")
	findDiagnostic(t, ds, diag.CodeBadEncoding, diag.ByteOrderMark.Text)
	if len(toks) != 1 || toks[0].Kind != token.EOF {
		t.Fatalf("tokens = %s, want only EOF", dump(toks))
	}
}

func TestEmptyFile(t *testing.T) {
	toks, ds := scan(t, "")
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
	}
	if len(toks) != 1 || toks[0].Kind != token.EOF {
		t.Fatalf("tokens = %s, want only EOF", dump(toks))
	}
}

func TestTabIsWhitespace(t *testing.T) {
	checkTokens(t, "a\tb\n", []wantToken{
		want(token.Ident, "a", "a"),
		want(token.Ident, "b", "b"),
		want(token.Newline, "\n", ""),
		want(token.EOF, "", ""),
	})
}
