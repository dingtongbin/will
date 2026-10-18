package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
)

func TestNestedTypeArguments(t *testing.T) {
	src := `func F(y: i64) {
    var a: Array<Array<i64>> = x
    var b: i64 = call<Array<i64>>(y)
    var c: Box<i64> = Box<i64> {
        value: 1,
    }
    var d: [4]u8 = x
    var e: []u8 = x
    var f: func(i32) -> bool = g
}
`
	f := parse(t, src)
	av := varDecl(t, f, "a")
	if av.Type.Name != "Array" || len(av.Type.Args) != 1 || av.Type.Args[0].Name != "Array" {
		t.Fatalf("a type = %+v, want Array<Array<i64>>", av.Type)
	}
	if len(av.Type.Args[0].Args) != 1 || av.Type.Args[0].Args[0].Name != "i64" {
		t.Fatalf("a inner type = %+v, want Array<i64>", av.Type.Args[0])
	}
	b, ok := varValue(t, f, "b").(*ast.CallExpr)
	if !ok || len(b.TypeArgs) != 1 || len(b.TypeArgs[0].Args) != 1 {
		t.Fatalf("b = %T, want call with Array<i64>", varValue(t, f, "b"))
	}
	c, ok := varValue(t, f, "c").(*ast.AggregateExpr)
	if !ok || len(c.TypeArgs) != 1 || len(c.Fields) != 1 {
		t.Fatalf("c = %T, want aggregate with type arguments", varValue(t, f, "c"))
	}
	dv := varDecl(t, f, "d")
	if dv.Type.Kind != ast.ArrayType || dv.Type.Len != "4" || dv.Type.Elem == nil {
		t.Fatalf("d type = %+v, want [4]u8", dv.Type)
	}
	ev := varDecl(t, f, "e")
	if ev.Type.Kind != ast.ViewType || ev.Type.Elem == nil {
		t.Fatalf("e type = %+v, want []u8", ev.Type)
	}
	fv := varDecl(t, f, "f")
	if fv.Type.Kind != ast.FuncType || len(fv.Type.Params) != 1 || fv.Type.Result == nil {
		t.Fatalf("f type = %+v, want func(i32) -> bool", fv.Type)
	}
}

func varDecl(t *testing.T, f *ast.File, name string) *ast.VarDecl {
	t.Helper()
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		for _, s := range fn.Body.Stmts {
			vs, ok := s.(*ast.VarStmt)
			if !ok {
				continue
			}
			vd, ok := vs.Decl.(*ast.VarDecl)
			if ok && vd.Name == name {
				return vd
			}
		}
	}
	t.Fatalf("variable %q not found", name)
	return nil
}
