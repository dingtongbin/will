package check

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func moves(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	return Moves(f)
}

func TestMoveRequiresACopyWhenTheSourceIsStillLive(t *testing.T) {
	src := `func F() {
    var s: str = "a"
    var t: str = s
    _ = s
    _ = t
}
`
	ds := moves(t, src)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want one:\n%s", len(ds), diag.FormatAll(ds))
	}
	d := ds[0]
	if d.Code != diag.CodeMissingClone {
		t.Errorf("code = %s, want O0002", d.Code)
	}
	if d.Message != "a copy is required here and cannot be proven" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "call Clone()" {
		t.Errorf("help = %q", d.Help)
	}
	if d.Pos.Line != 3 || d.Pos.Col != 18 {
		t.Errorf("position = %d:%d, want the transfer at 3:18", d.Pos.Line, d.Pos.Col)
	}
	if len(d.Notes) != 0 {
		t.Errorf("O0002 carries notes: %+v", d.Notes)
	}
	for _, x := range ds {
		if x.Code == diag.CodeMoved {
			t.Errorf("the use was reported as well:\n%s", diag.FormatAll(ds))
		}
	}
}

func TestMoveLastUseIsAccepted(t *testing.T) {
	src := `func F() {
    var s: str = "a"
    var t: str = s
    _ = t
}
`
	if ds := moves(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestMoveLoopCarriedUse(t *testing.T) {
	src := `func G(s: str) {
}

func F(xs: []i32) {
    var s: str = "a"
    for i in xs {
        _ = i
        _ = s
        var t: str = s
        _ = t
    }
}
`
	ds := moves(t, src)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want one:\n%s", len(ds), diag.FormatAll(ds))
	}
	d := ds[0]
	if d.Code != diag.CodeMoved || d.Message != "this value has been moved" {
		t.Errorf("diagnostic = %+v", d)
	}
	if d.Help != "call Clone() to keep a copy, or take a borrow" {
		t.Errorf("help = %q", d.Help)
	}
	if d.Pos.Line != 8 || d.Pos.Col != 13 {
		t.Errorf("position = %d:%d, want the use at 8:13", d.Pos.Line, d.Pos.Col)
	}
	if len(d.Notes) != 1 || d.Notes[0].Message != "this value moved here" {
		t.Fatalf("notes = %+v, want one moved-here note", d.Notes)
	}
	if d.Notes[0].Pos.Line != 9 || d.Notes[0].Pos.Col != 22 {
		t.Errorf("note = %d:%d, want the transfer at 9:22", d.Notes[0].Pos.Line, d.Notes[0].Pos.Col)
	}
}

func TestMoveIgnoresCopiedValues(t *testing.T) {
	src := `func F() {
    var n: i32 = 1
    var m: i32 = n
    _ = n
    _ = m
}
`
	if ds := moves(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestMoveTwoUsesInOneAggregate(t *testing.T) {
	src := `struct Pair {
    a: str
    b: str
}

func F() {
    var s: str = "a"
    var p: Pair = Pair { a: s, b: s }
    _ = p
}
`
	ds := moves(t, src)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want one:\n%s", len(ds), diag.FormatAll(ds))
	}
	d := ds[0]
	if d.Code != diag.CodeMoved || d.Pos.Line != 8 || d.Pos.Col != 35 {
		t.Errorf("diagnostic = %+v, want the second field", d)
	}
	if len(d.Notes) != 1 || d.Notes[0].Pos.Line != 8 || d.Notes[0].Pos.Col != 29 {
		t.Errorf("notes = %+v, want the first field", d.Notes)
	}
}

// TestMovePassingIsABorrow checks the settled convention: a parameter borrows
// its value, so passing it on is not a move and cannot require a copy.
func TestMoveAcrossAConditionalBranch(t *testing.T) {
	src := `func F(flag: bool) {
    var s: str = "a"
    if flag {
        var t: str = s
        _ = t
    }
    _ = s
}
`
	ds := moves(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeMoved {
		t.Fatalf("diagnostics = %s, want one O0001", diag.FormatAll(ds))
	}
	if ds[0].Pos.Line != 7 {
		t.Fatalf("position = %d, want the use after the branch", ds[0].Pos.Line)
	}
}

func TestMoveOwnershipFollowsStructFields(t *testing.T) {
	src := `struct Box {
    xs: Array<i32>
}

func F() {
    var a: Box = Box { xs: new Array<i32>(0) }
    var b: Box = a
    _ = a
    _ = b
}
`
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	ds = MovesWithOwnership(f, NewOwnership([]*ast.File{f}))
	if len(ds) != 1 || ds[0].Code != diag.CodeMissingClone {
		t.Fatalf("diagnostics = %s, want one O0002", diag.FormatAll(ds))
	}
}

func TestMoveOwnershipSubstitutesGenericFields(t *testing.T) {
	src := `struct Box<T> {
    value: T
}

func F() {
    var a: Box<Array<i32>> = Box { value: new Array<i32>(0) }
    var b: Box<Array<i32>> = a
    _ = a
    _ = b
}
`
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	ds = MovesWithOwnership(f, NewOwnership([]*ast.File{f}))
	if len(ds) != 1 || ds[0].Code != diag.CodeMissingClone {
		t.Fatalf("diagnostics = %s, want one O0002", diag.FormatAll(ds))
	}
}

func TestMovePassingIsABorrow(t *testing.T) {
	src := `func G(s: str) {
}

func F() {
    var s: str = "a"
    G(s)
    G(s)
}
`
	if ds := moves(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestMoveReturnIsAccepted(t *testing.T) {
	src := `func Take() -> str {
    var s: str = "a"
    return s
}
`
	if ds := moves(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}
