package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
)

func TestNestedTypeArguments(t *testing.T) {
	src := `def f(y: i64) {
    var a: list[list[i64]] = x
    var b: i64 = call<list[i64]>(y)
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
	if av.Type.Name != "list" || len(av.Type.Args) != 1 || av.Type.Args[0].Name != "list" {
		t.Fatalf("a type = %+v, want list[list[i64]]", av.Type)
	}
	if len(av.Type.Args[0].Args) != 1 || av.Type.Args[0].Args[0].Name != "i64" {
		t.Fatalf("a inner type = %+v, want list[i64]", av.Type.Args[0])
	}
	b, ok := varValue(t, f, "b").(*ast.CallExpr)
	if !ok || len(b.TypeArgs) != 1 || len(b.TypeArgs[0].Args) != 1 {
		t.Fatalf("b = %T, want call with list[i64]", varValue(t, f, "b"))
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

func TestContainerTypeArgumentsUseBrackets(t *testing.T) {
	cases := []string{
		"def f() { var xs: list<i32> = list<i32>() }\n",
		"def f() { var xs: dict<str, i32> = dict<str, i32>() }\n",
		"def f() { var xs: set<i32> = set<i32>() }\n",
	}
	for _, src := range cases {
		_, ds := Parse("test.will", []byte(src))
		if len(ds) == 0 {
			t.Errorf("source parsed without diagnostics:\n%s", src)
		}
	}
}

func TestDecorators(t *testing.T) {
	src := `@derive(Json, Show)
struct P {
    x: i32
}

@test
def test_one() {
}
`
	f := parse(t, src)
	if len(f.Decls) != 4 {
		t.Fatalf("declarations = %d, want 4", len(f.Decls))
	}
	if _, ok := f.Decls[0].(*ast.StructDecl); !ok {
		t.Fatalf("decl 0 = %T, want struct", f.Decls[0])
	}
	if d, ok := f.Decls[1].(*ast.DeriveDecl); !ok || d.Name != "Json" {
		t.Fatalf("decl 1 = %+v, want derive Json", f.Decls[1])
	}
	if d, ok := f.Decls[2].(*ast.DeriveDecl); !ok || d.Name != "Show" {
		t.Fatalf("decl 2 = %+v, want derive Show", f.Decls[2])
	}
	fn, ok := f.Decls[3].(*ast.FuncDecl)
	if !ok || !fn.Test {
		t.Fatalf("decl 3 = %+v, want test function", f.Decls[3])
	}
}

func TestSliceSyntax(t *testing.T) {
	src := `def f(xs: []i32) {
    var a: []i32 = xs[1:3]
    var b: []i32 = xs[:2]
    var c: []i32 = xs[1:]
    var d: []i32 = xs[:]
}
`
	f := parse(t, src)
	fn := firstFunc(t, f)
	want := []string{"a", "b", "c", "d"}
	for i, name := range want {
		vs, ok := fn.Body.Stmts[i].(*ast.VarStmt)
		if !ok {
			t.Fatalf("statement %d = %T, want var", i, fn.Body.Stmts[i])
		}
		d, ok := vs.Decl.(*ast.VarDecl)
		if !ok || d.Name != name {
			t.Fatalf("statement %d decl = %+v, want %s", i, vs.Decl, name)
		}
		sl, ok := d.Value.(*ast.SliceExpr)
		if !ok {
			t.Fatalf("%s value = %T, want slice", name, d.Value)
		}
		if sl.Lo == nil || sl.Hi == nil {
			t.Errorf("%s bounds = %+v, want both filled", name, sl)
		}
	}
}

func TestSliceRejectsStep(t *testing.T) {
	_, ds := Parse("test.will", []byte("def f(xs: []i32) { var v = xs[1:2:3] }\n"))
	if len(ds) == 0 {
		t.Fatal("a slice with a step parsed")
	}
}

func TestQualifiedNames(t *testing.T) {
	src := `import "std/net/http"
def f() -> http.Ctx {
    var c: http.Ctx = http.Ctx { host: "x" }
    return c
}
`
	f := parse(t, src)
	fn := firstFunc(t, f)
	if fn.Result == nil || fn.Result.Name != "http.Ctx" {
		t.Fatalf("result type = %+v, want http.Ctx", fn.Result)
	}
	c := varDecl(t, f, "c")
	if c.Type.Name != "http.Ctx" {
		t.Fatalf("c type = %+v, want http.Ctx", c.Type)
	}
	agg, ok := c.Value.(*ast.AggregateExpr)
	if !ok || agg.TypeName != "http.Ctx" {
		t.Fatalf("c value = %T, want aggregate http.Ctx", c.Value)
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
