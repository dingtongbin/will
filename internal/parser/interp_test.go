package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

func TestInterpolatedLiterals(t *testing.T) {
	src := `func F(n: i32) {
    var s: str = f"a {n + 1} b {{x}}"
    var p: str = f"plain"
    var e: str = f"{n}{n}"
}
`
	f := parse(t, src)
	s, ok := varValue(t, f, "s").(*ast.InterpExpr)
	if !ok || len(s.Parts) != 3 {
		t.Fatalf("s = %T, want three parts", varValue(t, f, "s"))
	}
	if s.Parts[0].Lit != "a " || s.Parts[2].Lit != " b {x}" {
		t.Errorf("s literals = %q and %q", s.Parts[0].Lit, s.Parts[2].Lit)
	}
	if _, ok := s.Parts[1].Expr.(*ast.BinaryExpr); !ok {
		t.Errorf("s expression = %T, want binary", s.Parts[1].Expr)
	}
	p, ok := varValue(t, f, "p").(*ast.InterpExpr)
	if !ok || len(p.Parts) != 1 || p.Parts[0].Lit != "plain" {
		t.Fatalf("p = %+v, want one literal part", varValue(t, f, "p"))
	}
	e, ok := varValue(t, f, "e").(*ast.InterpExpr)
	if !ok || len(e.Parts) != 2 {
		t.Fatalf("e = %+v, want two expression parts", varValue(t, f, "e"))
	}
	if e.Parts[0].Expr == nil || e.Parts[1].Expr == nil {
		t.Errorf("e parts have a nil expression")
	}
}

func TestEmptyInterpolationDiagnostic(t *testing.T) {
	_, ds := Parse("test.will", []byte("func F() {\n    var s: str = f\"{}\"\n}\n"))
	d := onlyDiagnostic(t, ds)
	if d.Code != diag.CodeUnexpectedToken || d.Message != "unexpected token" || d.Help != "expected an expression" {
		t.Fatalf("diagnostic = %+v", d)
	}
}
