package check

import (
	"testing"

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

func TestBorrowRejectsAMutCallWhileAViewIsLive(t *testing.T) {
	src := `struct Bag {
    xs: Array<i32>
}

func (mut b: Bag) Add() {
}

func F() {
    var b: Bag = Bag { xs: new Array<i32>(0)}
    var v: []i32 = b.xs[Range(0, b.xs.Len())]
    b.Add()
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
	src := `func Fill(mut xs: Array<i32>) {
}

func F() {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.Len())]
    Fill(xs)
    _ = v
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeBorrowConflict {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
}

func TestBorrowRejectsAMoveWhileAViewIsLive(t *testing.T) {
	src := `func F() -> Array<i32> {
    var xs: Array<i32> = new Array<i32>(0)
    var v: []i32 = xs[Range(0, xs.Len())]
    _ = v
    return xs
}
`
	ds := borrows(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeMoveWhileBorrowed {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
}

func TestBorrowEndsWithItsBlock(t *testing.T) {
	src := `func Fill(mut xs: Array<i32>) {
}

func F() {
    var xs: Array<i32> = new Array<i32>(0)
    if true {
        var v: []i32 = xs[Range(0, xs.Len())]
        _ = v
    }
    Fill(xs)
}
`
	if ds := borrows(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
}
