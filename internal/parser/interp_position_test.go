package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/source"
)

// TestInterpExpressionPositions checks the rule for interpolation: an
// expression inside {} is positioned at the first non-whitespace byte after
// the brace, counted in the bytes of the source line.
func TestInterpExpressionPositions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want source.Pos
	}{
		{"next to the brace", "def F(n: i32) -> str {\n    return f\"v={n}\"\n}\n", source.Pos{Line: 2, Col: 17}},
		{"space after the brace", "def F(n: i32) -> str {\n    return f\"v={ n }\"\n}\n", source.Pos{Line: 2, Col: 18}},
		{"escaped braces stick to the source", "def F(n: i32) -> str {\n    return f\"{{ {n}\"\n}\n", source.Pos{Line: 2, Col: 18}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, ds := Parse("test.will", []byte(c.src))
			if len(ds) != 0 {
				t.Fatalf("parse: %d diagnostics", len(ds))
			}
			fn := f.Decls[0].(*ast.FuncDecl)
			ret := fn.Body.Stmts[0].(*ast.ReturnStmt)
			interp, ok := ret.Value.(*ast.InterpExpr)
			if !ok {
				t.Fatalf("value = %T, want an interp expression", ret.Value)
			}
			var expr ast.Expr
			for _, part := range interp.Parts {
				if part.Expr != nil {
					expr = part.Expr
				}
			}
			if expr == nil {
				t.Fatal("no expression part")
			}
			got := expr.Pos()
			if got.Line != c.want.Line || got.Col != c.want.Col {
				t.Errorf("expression at %d:%d, want %d:%d", got.Line, got.Col, c.want.Line, c.want.Col)
			}
			if got.File != "test.will" {
				t.Errorf("file = %q, want test.will", got.File)
			}
		})
	}
}
