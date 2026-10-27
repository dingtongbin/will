package check

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func borrows(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	return Borrows(f)
}

func TestBorrowSeesAMutMethodInAnotherFile(t *testing.T) {
	decl, ds := parser.Parse("decl.will", []byte(`struct C {
    n: i32
}

def (mut c: C) bump() {
    c.n += 1
}

struct S {
    c: C
}
`))
	if len(ds) != 0 {
		t.Fatalf("parse decl: %s", diag.FormatAll(ds))
	}
	caller, ds := parser.Parse("caller.will", []byte(`def f(s: S) {
    var x: C = s.c
    x.bump()
}
`))
	if len(ds) != 0 {
		t.Fatalf("parse caller: %s", diag.FormatAll(ds))
	}
	files := []*ast.File{decl, caller}
	own := NewOwnership(files)
	effects := NewMutEffects(files)
	got := BorrowsWithEffects(caller, own, effects)
	if len(got) != 1 || got[0].Code != diag.CodeCopyNotBorrow {
		t.Fatalf("diagnostics = %s, want one O0008", diag.FormatAll(got))
	}
	if got[0].Message != "this is a copy, not a borrow" {
		t.Fatalf("message = %q", got[0].Message)
	}
}

func TestBorrowRejectsAMutCallWhileAViewIsLive(t *testing.T) {
	src := `struct Bag {
    xs: Array<i32>
}

def (mut b: Bag) add() {
}

def f() {
    var b: Bag = Bag { xs: new Array<i32>(0)}
    var v: []i32 = b.xs[Range(0, b.xs.len())]
    b.add()
    _ = v
}
`
	ds := borrows(t, src)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want one:\n%s", len(ds), diag.FormatAll(ds))
	}
	if ds[0].Code != diag.CodeBorrowConflict {
		t.Fatalf("code = %s, want O0003", ds[0].Code)
	}
	if ds[0].Message != "cannot borrow b mutably while it is borrowed" {
		t.Fatalf("message = %q", ds[0].Message)
	}
	if len(ds[0].Notes) != 1 || ds[0].Notes[0].Message != "borrowed here" {
		t.Fatalf("notes = %+v", ds[0].Notes)
	}
}

func TestBorrowRejectsAMutParameterWhileAViewIsLive(t *testing.T) {
	src := `def fill(mut xs: Array<i32>) {
}

def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    fill(xs)
    _ = v
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowConflict {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
}

func TestBorrowRejectsAMoveWhileAViewIsLive(t *testing.T) {
	src := `def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    var moved: Array<i32> = xs
    _ = v
    _ = moved
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeMoveWhileBorrowed {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
}

func TestBorrowRejectsContainerGrowthWhileAViewIsLive(t *testing.T) {
	src := `def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    xs.push(1)
    _ = v
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeContainerChanged {
		t.Fatalf("diagnostics = %s, want one O0007", diag.FormatAll(ds))
	}
	if ds[0].Message != "cannot change xs while it is borrowed" {
		t.Fatalf("message = %q", ds[0].Message)
	}
}

func TestBorrowEndsAtTheViewsLastUse(t *testing.T) {
	src := `def fill(mut xs: Array<i32>) {
}

def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    _ = v
    fill(xs)
}
`
	if ds := borrows(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %s, want none", diag.FormatAll(ds))
	}
}

func TestBorrowRejectsReturningALocalView(t *testing.T) {
	src := `def leak() -> []i32 {
    var xs: Array<i32> = new Array<i32>(0)
    return xs[Range(0, xs.len())]
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowOutlives {
		t.Fatalf("diagnostics = %s, want one O0004", diag.FormatAll(ds))
	}
	if ds[0].Message != "this borrow outlives xs" {
		t.Fatalf("message = %q", ds[0].Message)
	}
}

func TestBorrowRejectsStoringALocalView(t *testing.T) {
	src := `struct Holder {
    view: []i32
}

def store() -> Holder {
    var xs: Array<i32> = new Array<i32>(0)
    return Holder { view: xs[Range(0, xs.len())] }
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowStored {
		t.Fatalf("diagnostics = %s, want one O0005", diag.FormatAll(ds))
	}
}

func TestBorrowCanReturnAParameterView(t *testing.T) {
	src := `def slice(xs: []i32) -> []i32 {
    return xs[Range(0, xs.len())]
}
`
	if ds := borrows(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %s, want none", diag.FormatAll(ds))
	}
}

func TestCopyOfPlaceRejectsMutUse(t *testing.T) {
	src := `struct P {
    n: i32
}

struct Q {
    p: P
}

def (mut p: P) move() {
}

def fill(mut p: P) {
}

def f(q: Q) {
    var a: P = q.p
    a.move()
    fill(a)
    _ = a
}
`
	ds := borrows(t, src)
	if len(ds) != 2 {
		t.Fatalf("diagnostics = %d, want two:\n%s", len(ds), diag.FormatAll(ds))
	}
	for _, d := range ds {
		if d.Code != diag.CodeCopyNotBorrow {
			t.Fatalf("code = %s, want O0008", d.Code)
		}
		if d.Message != "this is a copy, not a borrow" {
			t.Fatalf("message = %q", d.Message)
		}
		if !strings.Contains(d.Help, "write through the place") {
			t.Fatalf("help = %q", d.Help)
		}
	}
}

func TestCopyOfScalarPlaceIsAllowed(t *testing.T) {
	src := `def fill(mut n: i32) {
}

def f(q: Q) {
    var n: i32 = q.n
    fill(n)
    _ = n
}
`
	if ds := borrows(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %s, want none", diag.FormatAll(ds))
	}
}

func TestBorrowRejectsReturningAClosureOfALocalView(t *testing.T) {
	src := `def leak() -> func() -> i32 {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    return func() -> i32 {
        return i32(v.len())
    }
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowOutlives {
		t.Fatalf("diagnostics = %s, want one O0004", diag.FormatAll(ds))
	}
	if ds[0].Message != "this borrow outlives xs" {
		t.Fatalf("message = %q", ds[0].Message)
	}
}

func TestBorrowRejectsReturningAClosureVariableOfALocalView(t *testing.T) {
	src := `def leak() -> func() -> i32 {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    var f: func() -> i32 = func() -> i32 { return i32(v.len()) }
    return f
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowOutlives {
		t.Fatalf("diagnostics = %s, want one O0004", diag.FormatAll(ds))
	}
}

func TestBorrowRejectsStoringAClosureOfALocalView(t *testing.T) {
	src := `struct Holder {
    cb: func() -> i32
}

def store() -> Holder {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    return Holder { cb: func() -> i32 { return i32(v.len()) } }
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowStored {
		t.Fatalf("diagnostics = %s, want one O0005", diag.FormatAll(ds))
	}
}

func TestBorrowRejectsSpawningAClosureOfALocalView(t *testing.T) {
	src := `def work() -> i32 {
	return 0
}

def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    var f: func() -> i32 = func() -> i32 { return i32(v.len()) }
    spawn f()
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowOutlives {
		t.Fatalf("diagnostics = %s, want one O0004", diag.FormatAll(ds))
	}
}

func TestBorrowAliasEndsAtItsOwnLastUse(t *testing.T) {
	src := `def fill(mut xs: Array<i32>) {
}

def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    var w: []i32 = v
    _ = w
    _ = v
    fill(xs)
}
`
	if ds := borrows(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %s, want none", diag.FormatAll(ds))
	}
}

func TestBorrowAliasKeepsOwnerBorrowed(t *testing.T) {
	src := `def fill(mut xs: Array<i32>) {
}

def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    var w: []i32 = v
    _ = v
    fill(xs)
    _ = w
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowConflict {
		t.Fatalf("diagnostics = %s, want one O0003", diag.FormatAll(ds))
	}
}

func TestBorrowNotPinnedByUnrelatedClosure(t *testing.T) {
	src := `def fill(mut xs: Array<i32>) {
}

def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    var f: func() -> i32 = func() -> i32 { return 1 }
    _ = f
    _ = v
    fill(xs)
}
`
	if ds := borrows(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %s, want none", diag.FormatAll(ds))
	}
}

func TestBorrowPinnedByCapturedView(t *testing.T) {
	src := `def fill(mut xs: Array<i32>) {
}

def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    var f: func() -> i32 = func() -> i32 {
        return i32(v.get(0))
    }
    _ = f
    _ = v
    fill(xs)
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowConflict {
		t.Fatalf("diagnostics = %s, want one O0003", diag.FormatAll(ds))
	}
}

func TestBorrowNotPinnedByUnrelatedDefer(t *testing.T) {
	src := `def tick() {
}

def fill(mut xs: Array<i32>) {
}

def f() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.len())]
    defer tick()
    _ = v
    fill(xs)
}
`
	if ds := borrows(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %s, want none", diag.FormatAll(ds))
	}
}

func TestBorrowEndsWithItsBlock(t *testing.T) {
	src := `def fill(mut xs: Array<i32>) {
}

def f() {
    var xs: Array<i32> = new Array<i32>(0)
    if True {
        var v: []i32 = xs[Range(0, xs.len())]
        _ = v
    }
    fill(xs)
}
`
	if ds := borrows(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
}
