package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

func findFunc(t *testing.T, f *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("function %q not found", name)
	return nil
}

func TestParseStatements(t *testing.T) {
	f := parse(t, program)
	run := findFunc(t, f, "Run")
	if len(run.Body.Stmts) != 16 {
		t.Fatalf("statements = %d, want 16", len(run.Body.Stmts))
	}
	loop, ok := run.Body.Stmts[6].(*ast.ForStmt)
	if !ok || len(loop.Names) != 2 || loop.Names[0] != "i" || loop.Names[1] != "x" || loop.Iter == nil {
		t.Fatalf("statement 6 = %+v, want for i, x in ...", run.Body.Stmts[6])
	}
	cond, ok := run.Body.Stmts[7].(*ast.IfExpr)
	if !ok {
		t.Fatalf("statement 7 = %T, want if", run.Body.Stmts[7])
	}
	chain, ok := cond.Else.(*ast.IfExpr)
	if !ok || chain.Else == nil {
		t.Fatalf("if else = %T, want else if with else", cond.Else)
	}
	match, ok := run.Body.Stmts[8].(*ast.MatchExpr)
	if !ok || len(match.Arms) != 3 {
		t.Fatalf("statement 8 = %+v, want match with three arms", run.Body.Stmts[8])
	}
	if match.Arms[0].Pattern.Kind != ast.LiteralPattern {
		t.Errorf("arm 0 pattern = %v, want literal", match.Arms[0].Pattern.Kind)
	}
	if match.Arms[1].Pattern.Kind != ast.LiteralPattern || match.Arms[1].Guard == nil {
		t.Errorf("arm 1 = %+v, want literal with guard", match.Arms[1])
	}
	if match.Arms[2].Pattern.Kind != ast.WildPattern {
		t.Errorf("arm 2 pattern = %v, want wild", match.Arms[2].Pattern.Kind)
	}
	region, ok := run.Body.Stmts[9].(*ast.RegionStmt)
	if !ok || len(region.Body.Stmts) != 2 {
		t.Fatalf("statement 9 = %+v, want region", run.Body.Stmts[9])
	}
	taskVar, ok := region.Body.Stmts[0].(*ast.VarStmt)
	if !ok {
		t.Fatalf("region statement 0 = %T, want var", region.Body.Stmts[0])
	}
	taskDecl, ok := taskVar.Decl.(*ast.VarDecl)
	if !ok {
		t.Fatalf("region declaration = %T, want var decl", taskVar.Decl)
	}
	if _, ok := taskDecl.Value.(*ast.SpawnExpr); !ok {
		t.Errorf("region task = %T, want spawn", taskDecl.Value)
	}
	sel, ok := run.Body.Stmts[10].(*ast.SelectStmt)
	if !ok || len(sel.Arms) != 3 {
		t.Fatalf("statement 10 = %+v, want select with three arms", run.Body.Stmts[10])
	}
	if sel.Arms[0].Bind != "v" || sel.Arms[0].Chan == nil {
		t.Errorf("select arm 0 = %+v, want bound channel", sel.Arms[0])
	}
	if sel.Arms[1].Timeout == nil {
		t.Errorf("select arm 1 = %+v, want timeout", sel.Arms[1])
	}
	if !sel.Arms[2].Default {
		t.Errorf("select arm 2 = %+v, want default", sel.Arms[2])
	}
	if _, ok := run.Body.Stmts[11].(*ast.ParallelStmt); !ok {
		t.Errorf("statement 11 = %T, want parallel", run.Body.Stmts[11])
	}
	dl, ok := run.Body.Stmts[12].(*ast.DeadlineExpr)
	if !ok || dl.Body == nil {
		t.Fatalf("statement 12 = %T, want deadline", run.Body.Stmts[12])
	}
	deferStmt, ok := run.Body.Stmts[13].(*ast.DeferStmt)
	if !ok || !isCallLike(deferStmt.Call) {
		t.Fatalf("statement 13 = %+v, want defer call", run.Body.Stmts[13])
	}
	exprStmt, ok := run.Body.Stmts[14].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("statement 14 = %T, want expression statement", run.Body.Stmts[14])
	}
	if call, ok := exprStmt.Expr.(*ast.CallExpr); !ok {
		t.Errorf("statement 14 expression = %T, want call", exprStmt.Expr)
	} else if _, ok := call.Callee.(*ast.LambdaExpr); !ok {
		t.Errorf("statement 14 callee = %T, want lambda", call.Callee)
	}
	if _, ok := run.Body.Stmts[15].(*ast.ReturnStmt); !ok {
		t.Errorf("statement 15 = %T, want return", run.Body.Stmts[15])
	}
}

func TestBlockValueAndNewlineRules(t *testing.T) {
	src := `func F(n: i32) {
    var a: i32 = Add(1,
        2)
    var b: i32 = 1 +
        2
    var c: i32 = if n > 0 { 1 } else { 2 }
    if (Point { x: 1 }) == p {
        Work()
    }
    if n > 0 {
        Work()
    } else {
        Stop()
    }
}
`
	f := parse(t, src)
	fn := firstFunc(t, f)
	if len(fn.Body.Stmts) != 5 {
		t.Fatalf("statements = %d, want 5", len(fn.Body.Stmts))
	}
	if _, ok := fn.Body.Stmts[0].(*ast.VarStmt); !ok {
		t.Errorf("statement 0 = %T, want var", fn.Body.Stmts[0])
	}
}

func TestMoreStatements(t *testing.T) {
	src := `func F() {
    var t: Task<i32> = spawn on Ui Fetch(1)
    var l: usize = lane.Here()
    _ = t.Join()
    p.x += 1
    p.x <<= 2
}
`
	f := parse(t, src)
	fn := firstFunc(t, f)
	if len(fn.Body.Stmts) != 5 {
		t.Fatalf("statements = %d, want 5", len(fn.Body.Stmts))
	}
	spawn, ok := varDecl(t, f, "t").Value.(*ast.SpawnExpr)
	if !ok || spawn.Place != "on" || spawn.Lane != "Ui" {
		t.Fatalf("t = %+v, want spawn on Ui", varDecl(t, f, "t").Value)
	}
	if _, ok := varDecl(t, f, "l").Value.(*ast.LaneHereExpr); !ok {
		t.Errorf("l = %T, want lane.Here()", varDecl(t, f, "l").Value)
	}
	discard, ok := fn.Body.Stmts[2].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("statement 2 = %T, want assignment", fn.Body.Stmts[2])
	}
	if name, ok := discard.Target.(*ast.NameExpr); !ok || name.Name != "_" {
		t.Errorf("discard target = %T, want _", discard.Target)
	}
	compound, ok := fn.Body.Stmts[3].(*ast.AssignStmt)
	if !ok || compound.Op != "+=" {
		t.Fatalf("statement 3 = %+v, want +=", fn.Body.Stmts[3])
	}
	if _, ok := compound.Target.(*ast.FieldExpr); !ok {
		t.Errorf("compound target = %T, want field", compound.Target)
	}
	shift, ok := fn.Body.Stmts[4].(*ast.AssignStmt)
	if !ok || shift.Op != "<<=" {
		t.Fatalf("statement 4 = %+v, want <<=", fn.Body.Stmts[4])
	}
}

func TestSpawnPlace(t *testing.T) {
	src := `func G() {
}

func F() {
    var a: Task<()> = spawn G()
    var b: Task<()> = spawn here G()
}
`
	f := parse(t, src)
	auto, ok := varDecl(t, f, "a").Value.(*ast.SpawnExpr)
	if !ok || auto.Place != "auto" || auto.Lane != "" {
		t.Fatalf("a = %+v, want spawn auto", varDecl(t, f, "a").Value)
	}
	here, ok := varDecl(t, f, "b").Value.(*ast.SpawnExpr)
	if !ok || here.Place != "here" || here.Lane != "" {
		t.Fatalf("b = %+v, want spawn here", varDecl(t, f, "b").Value)
	}
}

func TestCallTryAndUnwrapAsStatements(t *testing.T) {
	src := `func F() -> Result<(), Error> {
    Check()?
    Check()!
    return Ok(())
}
`
	f := parse(t, src)
	fn := firstFunc(t, f)
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("statements = %d, want 3", len(fn.Body.Stmts))
	}
	first, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("statement 0 = %T, want expression statement", fn.Body.Stmts[0])
	}
	if _, ok := first.Expr.(*ast.TryExpr); !ok {
		t.Errorf("statement 0 expression = %T, want try", first.Expr)
	}
	second, ok := fn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("statement 1 = %T, want expression statement", fn.Body.Stmts[1])
	}
	if _, ok := second.Expr.(*ast.UnwrapExpr); !ok {
		t.Errorf("statement 1 expression = %T, want unwrap", second.Expr)
	}
}

func TestSuffixDoesNotExtendDeferOrSpawn(t *testing.T) {
	// A defer still reports the generic shape; a spawn operand rule has its
	// own code (K0005) because the concurrency volume fixes it.
	_, ds := Parse("test.will", []byte("func F() {\n    defer G()?\n}\n"))
	if len(ds) != 1 || ds[0].Help != "expected a call" {
		t.Fatalf("defer diagnostics = %+v, want one expected-call error", ds)
	}
	_, ds = Parse("test.will", []byte("func F() {\n    spawn G()?\n}\n"))
	if len(ds) != 1 || ds[0].Code != diag.CodeSpawnNotCall {
		t.Fatalf("spawn diagnostics = %+v, want one K0005", ds)
	}
}

func TestNewlineBeforeDotEndsStatement(t *testing.T) {
	src := `func F() {
    p
    .x
}
`
	_, ds := Parse("test.will", []byte(src))
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want 1:\n%s", len(ds), diag.FormatAll(ds))
	}
	if ds[0].Message != "unexpected token" || ds[0].Help != "expected a call or spawn" {
		t.Fatalf("diagnostic = %+v, want unexpected token for the bare name", ds[0])
	}
}
