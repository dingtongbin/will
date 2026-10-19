package check

import (
	"testing"

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
	src := `func G(s: str) {
}

func F() {
    var s: str = "a"
    G(s)
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
        _ = s
        G(s)
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
	if d.Pos.Line != 7 || d.Pos.Col != 13 {
		t.Errorf("position = %d:%d, want the use at 7:13", d.Pos.Line, d.Pos.Col)
	}
	if len(d.Notes) != 1 || d.Notes[0].Message != "this value moved here" {
		t.Fatalf("notes = %+v, want one moved-here note", d.Notes)
	}
	if d.Notes[0].Pos.Line != 8 || d.Notes[0].Pos.Col != 11 {
		t.Errorf("note = %d:%d, want the transfer at 8:11", d.Notes[0].Pos.Line, d.Notes[0].Pos.Col)
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

func TestMoveTwoUsesInOneCall(t *testing.T) {
	src := `func G(a: str, b: str) {
}

func F() {
    var s: str = "a"
    G(s, s)
}
`
	ds := moves(t, src)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want one:\n%s", len(ds), diag.FormatAll(ds))
	}
	d := ds[0]
	if d.Code != diag.CodeMoved || d.Pos.Line != 6 || d.Pos.Col != 10 {
		t.Errorf("diagnostic = %+v, want the second argument", d)
	}
	if len(d.Notes) != 1 || d.Notes[0].Pos.Line != 6 || d.Notes[0].Pos.Col != 7 {
		t.Errorf("notes = %+v, want the first argument", d.Notes)
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
