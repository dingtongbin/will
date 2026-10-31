package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

func parse(t *testing.T, src string) *ast.File {
	t.Helper()
	f, ds := Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
	}
	if f == nil {
		t.Fatal("file is nil")
	}
	return f
}

const program = `import "std/net/http"
import _ "std/bytes"
import c "stdio.h" {
    fast puts
    blocking read
}

const Limit: i32 = 1_000
var counter: i64 = 0

@derive(Show)
struct Point {
    x: i32
    y: i32 = 0

    def move(self, dx: i32, dy: i32) {
        self.x += dx
        self.y += dy
    }
}

enum Shape {
    Circle(f64)
    Square
}

interface Showable {
    def show() -> str
}


def max<T: Ord>(a: T, b: T) -> T {
    if a > b {
        return a
    }
    return b
}

def run(n: i32) -> Result<i32, Error> {
    var total: i32 = 0
    var p: Point = Point { x: 1, y: n }
    var text: str = f"value {n} {{ok}}"
    var ch: chan<i32> = chan<i32>(8)
    var box: Box<i64> = Box<i64>(5)
    var cb: func(i32) -> bool = func(x: i32) -> bool { return x > 0 }
    for i, x in items {
        total += x
    }
    if total == 0 {
        return Err(NotFound)
    } else if total > 10 {
        total = 10
    } else {
        total = total * 2
    }
    match total {
        0 => { total = 0 }
        9 if total > 2 => { total = 3 }
        _ => { total = 4 }
    }
    region {
        var t: Task<i32> = spawn fetch(n)
        _ = await t
    }
    select {
        v = ch.recv() => { total = v }
        timeout 5ms => { total = -1 }
        default => { total = 0 }
    }
    deadline 3ms {
        work()
    }
    defer Cleanup()
    func() { work() }()
    return Ok(total)
}
`

func firstFunc(t *testing.T, f *ast.File) *ast.FuncDecl {
	t.Helper()
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			return fn
		}
	}
	t.Fatal("no function declaration")
	return nil
}

func varValue(t *testing.T, f *ast.File, name string) ast.Expr {
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
				return vd.Value
			}
		}
	}
	t.Fatalf("variable %q not found", name)
	return nil
}

func TestParseProgram(t *testing.T) {
	f := parse(t, program)
	if len(f.Imports) != 3 {
		t.Fatalf("imports = %d, want 3", len(f.Imports))
	}
	if f.Imports[0].Path != "std/net/http" || f.Imports[0].C || f.Imports[0].SideEffect {
		t.Errorf("first import = %+v", f.Imports[0])
	}
	if !f.Imports[1].SideEffect || f.Imports[1].Path != "std/bytes" {
		t.Errorf("second import = %+v", f.Imports[1])
	}
	if !f.Imports[2].C || f.Imports[2].Path != "stdio.h" || len(f.Imports[2].Links) != 2 {
		t.Fatalf("third import = %+v", f.Imports[2])
	}
	if !f.Imports[2].Links[0].Fast || f.Imports[2].Links[0].Name != "puts" {
		t.Errorf("first link = %+v", f.Imports[2].Links[0])
	}
	if !f.Imports[2].Links[1].Blocking || f.Imports[2].Links[1].Name != "read" {
		t.Errorf("second link = %+v", f.Imports[2].Links[1])
	}
	var (
		point  *ast.StructDecl
		derive *ast.DeriveDecl
		shape  *ast.EnumDecl
		iface  *ast.InterfaceDecl
		move   *ast.FuncDecl
		max    *ast.FuncDecl
		run    *ast.FuncDecl
	)
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.StructDecl:
			if x.Name == "Point" {
				point = x
			}
		case *ast.DeriveDecl:
			if x.Name == "Show" {
				derive = x
			}
		case *ast.EnumDecl:
			if x.Name == "Shape" {
				shape = x
			}
		case *ast.InterfaceDecl:
			if x.Name == "Showable" {
				iface = x
			}
		case *ast.FuncDecl:
			switch x.Name {
			case "move":
				move = x
			case "max":
				max = x
			case "run":
				run = x
			}
		}
	}
	if point == nil || len(point.Fields) != 2 || point.Fields[1].Default == nil {
		t.Fatalf("point = %+v, want Point with a default", point)
	}
	if derive == nil {
		t.Fatalf("derive Show missing")
	}
	if shape == nil || len(shape.Variants) != 2 || len(shape.Variants[0].Args) != 1 {
		t.Fatalf("shape = %+v, want Shape", shape)
	}
	if iface == nil || len(iface.Methods) != 1 || iface.Methods[0].Name != "show" {
		t.Fatalf("iface = %+v, want Showable", iface)
	}
	if move == nil || move.Recv == nil || move.Name != "move" {
		t.Fatalf("move = %+v, want method move", move)
	}
	if max == nil || len(max.TypeParams) != 1 || len(max.TypeParams[0].Constraints) != 1 {
		t.Fatalf("max = %+v, want generic Max", max)
	}
	if run == nil {
		t.Fatalf("run missing")
	}
}

func TestAsyncFunctionAndAwait(t *testing.T) {
	f := parse(t, "async def load() -> i32 {\n    return await read()?\n}\n")
	fn, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok || !fn.Async {
		t.Fatalf("decl 0 = %+v, want async Load", f.Decls[0])
	}
	ret, ok := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("statement = %T, want return", fn.Body.Stmts[0])
	}
	try, ok := ret.Value.(*ast.TryExpr)
	if !ok {
		t.Fatalf("return value = %T, want ? around await", ret.Value)
	}
	await, ok := try.X.(*ast.AwaitExpr)
	if !ok {
		t.Fatalf("? operand = %T, want await", try.X)
	}
	if _, ok := await.X.(*ast.CallExpr); !ok {
		t.Fatalf("await operand = %T, want call", await.X)
	}
}
