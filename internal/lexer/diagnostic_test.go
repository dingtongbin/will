package lexer

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
)

func TestDiagnosticFormat(t *testing.T) {
	_, ds := scan(t, ";\n")
	d := findDiagnostic(t, ds, diag.CodeBadSymbol, diag.BadSymbol.Text)
	got := diag.Format(d)
	want := "test.will:1:1: L0006 this symbol is not part of the language\n" +
		"    help: remove it\n"
	if got != want {
		t.Fatalf("format = %q, want %q", got, want)
	}
}

func TestDiagnosticsAreSorted(t *testing.T) {
	_, ds := scan(t, "@ $\n")
	if len(ds) != 2 {
		t.Fatalf("diagnostics = %d, want 2:\n%s", len(ds), diag.FormatAll(ds))
	}
	if ds[0].Pos.Col != 1 || ds[1].Pos.Col != 3 {
		t.Fatalf("columns = %d,%d, want 1,3", ds[0].Pos.Col, ds[1].Pos.Col)
	}
}
