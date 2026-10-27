package check

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func TestLambdaCapturesNames(t *testing.T) {
	src := `struct C {
    n: i32
}

def f() -> i32 {
    var c: rc<C> = new rc<C>(C { n: 1 })
    var f: func() -> i32 = func() -> i32 {
        return c.n
    }
    _ = f
    return c.n
}
`
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fdecl, ok := d.(*ast.FuncDecl); ok {
			fn = fdecl
			break
		}
	}
	if fn == nil {
		t.Fatal("function not found")
	}
	for _, st := range fn.Body.Stmts {
		vs, ok := st.(*ast.VarStmt)
		if !ok {
			continue
		}
		vd, ok := vs.Decl.(*ast.VarDecl)
		if !ok {
			continue
		}
		lam, ok := vd.Value.(*ast.LambdaExpr)
		if !ok {
			continue
		}
		got := lambdaCaptures(lam)
		if len(got) != 1 || got[0].name != "c" {
			t.Fatalf("captures = %+v, want c", got)
		}
		return
	}
	t.Fatal("lambda not found")
}

// TestClosureMovesOwnedLocal checks the capture rule in semantics: a closure
// takes an owned local by move, so a later use is O0001.
func TestClosureMovesOwnedLocal(t *testing.T) {
	src := `def f() -> i32 {
    var text: str = "hello"
    var f: func() -> usize = func() -> usize {
        return text.len()
    }
    _ = f
    return i32(text.len())
}
`
	ds := movesOwned(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeMissingClone {
		t.Fatalf("diagnostics = %s, want one O0002", diag.FormatAll(ds))
	}
}

// TestClosureCanMoveDeadOwnedLocal checks the normal path: no later use means
// the local moves into the environment without a clone.
func TestClosureCanMoveDeadOwnedLocal(t *testing.T) {
	src := `def f() -> i32 {
    var text: str = "hello"
    var f: func() -> usize = func() -> usize {
        return text.len()
    }
    return i32(f())
}
`
	if ds := movesOwned(t, src); len(ds) != 0 {
		t.Fatalf("a dead local did not move:\n%s", diag.FormatAll(ds))
	}
}

// TestSecondClosureCaptureNeedsClone checks that two closures cannot move the
// same local; the second capture is a use of the moved value.
func TestSecondClosureCaptureNeedsClone(t *testing.T) {
	src := `def f() -> i32 {
    var text: str = "hello"
    var f: func() -> usize = func() -> usize {
        return text.len()
    }
    var g: func() -> usize = func() -> usize {
        return text.len()
    }
    return i32(f() + g())
}
`
	ds := movesOwned(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeMoved {
		t.Fatalf("diagnostics = %s, want one O0001", diag.FormatAll(ds))
	}
}

// TestClosureCopiesRcHandle checks that an rc handle stays copyable: the
// closure retains it and the source remains usable.
func TestClosureCopiesRcHandle(t *testing.T) {
	src := `struct C {
    n: i32
}

def f() -> i32 {
    var c: rc<C> = new rc<C>(C { n: 1 })
    var f: func() -> i32 = func() -> i32 {
        return c.n
    }
    return f() + c.n
}
`
	if ds := movesOwned(t, src); len(ds) != 0 {
		t.Fatalf("an rc handle was moved:\n%s", diag.FormatAll(ds))
	}
}

// TestClosureCaptureInLoopNeedsFreshOwner checks that a capture inside a loop
// cannot move a local declared outside it: the next iteration would see an
// empty slot.
func TestClosureCaptureInLoopNeedsFreshOwner(t *testing.T) {
	src := `def f() -> i32 {
    var text: str = "hello" + "!"
    var f: func() -> usize = func() -> usize {
        return 0
    }
    for _ in Range(0, 2) {
        f = func() -> usize {
            return text.len()
        }
    }
    return i32(f())
}
`
	ds := movesOwned(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeMissingClone {
		t.Fatalf("diagnostics = %s, want one O0002", diag.FormatAll(ds))
	}
}

// TestClosureCaptureInLoopWithFreshLocal checks the legal form: a local
// declared inside the loop is recreated for every capture.
func TestClosureCaptureInLoopWithFreshLocal(t *testing.T) {
	src := `def f() -> i32 {
    var text: str = "hello" + "!"
    var f: func() -> usize = func() -> usize {
        return 0
    }
    for _ in Range(0, 2) {
        var inner: str = text.clone()
        f = func() -> usize {
            return inner.len()
        }
    }
    return i32(f())
}
`
	if ds := movesOwned(t, src); len(ds) != 0 {
		t.Fatalf("a fresh loop local was rejected:\n%s", diag.FormatAll(ds))
	}
}

// TestNestedClosureMovesItsOwnLocal checks that a closure created inside
// another closure may move a local declared in the immediate body, while a
// capture from the outer environment stays a copy.
func TestNestedClosureMovesItsOwnLocal(t *testing.T) {
	src := `def f() -> i32 {
    var make: func() -> func() -> usize = func() -> func() -> usize {
        var inner: str = "abc" + "d"
        return func() -> usize {
            return inner.len()
        }
    }
    var g: func() -> usize = make()
    return i32(g())
}
`
	if ds := movesOwned(t, src); len(ds) != 0 {
		t.Fatalf("a nested local did not move:\n%s", diag.FormatAll(ds))
	}
}

// TestClosureCapturesParameterByCopy checks that a borrowed parameter is not
// moved out of the caller; the closure keeps the clone path.
func TestClosureCapturesParameterByCopy(t *testing.T) {
	src := `struct C {
    n: i32
}

def make(c: rc<C>) -> func() -> i32 {
    return func() -> i32 {
        return c.n
    }
}
`
	if ds := movesOwned(t, src); len(ds) != 0 {
		t.Fatalf("a borrowed parameter was treated as a move:\n%s", diag.FormatAll(ds))
	}
}
