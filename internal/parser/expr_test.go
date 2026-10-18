package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
)

const exprProgram = `func F(n: i32) {
    var a: i32 = 1 + 2 * 3
    var b: bool = a < 4 && n > 0
    var c: i32 = -a
    var d: i32 = a?
    var e: i32 = xs[0]
    var f: []i32 = xs[1..3]
    var g: Point = Point { x: 1, y: 2 }
    var h: i32 = p.x
    var i: i32 = p.Move(1, 2)
    var j: i32 = call<i64>(n)
    var k: i32 = func(x: i32) -> i32 { return x }(1)
    var l: i32 = if n > 0 { 1 } else { 2 }
    var m: i32 = match n {
        0 => 1
        _ => 2
    }
    var o: chan<i32> = new chan<i32>(8)
    var q: i32 = if n > 0 { 1 } else if n < 0 { -1 } else { 0 }
}
`

func TestParseExpressions(t *testing.T) {
	f := parse(t, exprProgram)

	a, ok := varValue(t, f, "a").(*ast.BinaryExpr)
	if !ok || a.Op.String() != "+" {
		t.Fatalf("a = %T, want +", varValue(t, f, "a"))
	}
	if r, ok := a.R.(*ast.BinaryExpr); !ok || r.Op.String() != "*" {
		t.Errorf("a right = %T, want *", a.R)
	}
	if b, ok := varValue(t, f, "b").(*ast.BinaryExpr); !ok || b.Op.String() != "&&" {
		t.Errorf("b = %T, want &&", varValue(t, f, "b"))
	}
	if c, ok := varValue(t, f, "c").(*ast.UnaryExpr); !ok || c.Op.String() != "-" {
		t.Errorf("c = %T, want unary -", varValue(t, f, "c"))
	}
	if _, ok := varValue(t, f, "d").(*ast.TryExpr); !ok {
		t.Errorf("d = %T, want try", varValue(t, f, "d"))
	}
	if _, ok := varValue(t, f, "e").(*ast.IndexExpr); !ok {
		t.Errorf("e = %T, want index", varValue(t, f, "e"))
	}
	if _, ok := varValue(t, f, "f").(*ast.SliceExpr); !ok {
		t.Errorf("f = %T, want slice", varValue(t, f, "f"))
	}
	g, ok := varValue(t, f, "g").(*ast.AggregateExpr)
	if !ok || len(g.Fields) != 2 || g.TypeName != "Point" {
		t.Fatalf("g = %T, want aggregate Point", varValue(t, f, "g"))
	}
	if _, ok := varValue(t, f, "h").(*ast.FieldExpr); !ok {
		t.Errorf("h = %T, want field", varValue(t, f, "h"))
	}
	if _, ok := varValue(t, f, "i").(*ast.MethodCallExpr); !ok {
		t.Errorf("i = %T, want method call", varValue(t, f, "i"))
	}
	j, ok := varValue(t, f, "j").(*ast.CallExpr)
	if !ok || len(j.TypeArgs) != 1 {
		t.Fatalf("j = %T, want call with one type argument", varValue(t, f, "j"))
	}
	k, ok := varValue(t, f, "k").(*ast.CallExpr)
	if !ok {
		t.Fatalf("k = %T, want call", varValue(t, f, "k"))
	}
	if _, ok := k.Callee.(*ast.LambdaExpr); !ok {
		t.Errorf("k callee = %T, want lambda", k.Callee)
	}
	l, ok := varValue(t, f, "l").(*ast.IfExpr)
	if !ok || l.Else == nil {
		t.Fatalf("l = %T, want if with else", varValue(t, f, "l"))
	}
	m, ok := varValue(t, f, "m").(*ast.MatchExpr)
	if !ok || len(m.Arms) != 2 {
		t.Fatalf("m = %T, want match with two arms", varValue(t, f, "m"))
	}
	o, ok := varValue(t, f, "o").(*ast.NewExpr)
	if !ok || o.Type.Name != "chan" || len(o.Type.Args) != 1 {
		t.Fatalf("o = %T, want new chan<i32>", varValue(t, f, "o"))
	}
	q, ok := varValue(t, f, "q").(*ast.IfExpr)
	if !ok {
		t.Fatalf("q = %T, want if", varValue(t, f, "q"))
	}
	if _, ok := q.Else.(*ast.IfExpr); !ok {
		t.Errorf("q else = %T, want else if", q.Else)
	}
}
