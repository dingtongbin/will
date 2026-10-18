package lexer

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/token"
)

func TestNumericAndDurationLiterals(t *testing.T) {
	src := "0 1_000 0xFF 0b1010 0o17 1i32 2u64 3usize 4.5 1e3 1.5e-3 2.5f64 1E+2 1f32 250ms 30s 2h 1_000us"
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
		want(token.Float, "1.5e-3", ""),
		want(token.Float, "2.5f64", ""),
		want(token.Float, "1E+2", ""),
		want(token.Float, "1f32", ""),
		want(token.Duration, "250ms", ""),
		want(token.Duration, "30s", ""),
		want(token.Duration, "2h", ""),
		want(token.Duration, "1_000us", ""),
		want(token.EOF, "", ""),
	})
}

func TestInvalidNumbers(t *testing.T) {
	cases := []struct {
		name string
		src  string
		col  int
	}{
		{"trailing dot", "1.", 2},
		{"leading dot", ".5", 1},
		{"empty hex", "0x", 3},
		{"digit outside binary", "0b102", 5},
		{"trailing separator", "1_", 2},
		{"double separator", "1__0", 2},
		{"base trailing separator", "0xFF_", 5},
		{"separator before suffix", "1_u8", 2},
		{"int suffix on float", "1.5i32", 4},
		{"duration suffix on float", "1.5ms", 4},
		{"unknown suffix", "123abc", 4},
		{"empty exponent", "1e", 2},
		{"unknown base digits", "0xGG", 3},
		{"separator in float integer part", "1_000.5", 2},
		{"separator in float exponent", "1e1_0", 4},
		{"uppercase base prefix", "0X1", 2},
		{"second dot", "1.2.3", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, ds := scan(t, c.src+"\n")
			d := findDiagnostic(t, ds, diag.CodeBadNumber, diag.InvalidNumber.Text)
			if d.Pos.Line != 1 || d.Pos.Col != c.col {
				t.Errorf("position = %d:%d, want 1:%d", d.Pos.Line, d.Pos.Col, c.col)
			}
			if d.Help != diag.InvalidNumber.Help {
				t.Errorf("help = %q, want %q", d.Help, diag.InvalidNumber.Help)
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

func TestDurationKeepsIntegerSeparators(t *testing.T) {
	toks, ds := scan(t, "1_000ms\n")
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
	}
	if toks[0].Kind != token.Duration || toks[0].Text != "1_000ms" {
		t.Fatalf("first token = %s %q, want duration 1_000ms", toks[0].Kind, toks[0].Text)
	}
}
