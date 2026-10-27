package check

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func topWrites(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	return TopLevelWrites(f)
}

func TestTopLevelWriteRejectsAssignment(t *testing.T) {
	src := `var counter: i32 = 0

def f() {
    counter = 1
}
`
	ds := topWrites(t, src)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want one:\n%s", len(ds), diag.FormatAll(ds))
	}
	if ds[0].Code != diag.CodeTopLevelWrite ||
		ds[0].Message != "top-level counter is published and read-only" ||
		ds[0].Help != "put the mutable state behind an rc and a guard" {
		t.Fatalf("diagnostic = %+v", ds[0])
	}
}

func TestTopLevelWriteRejectsContainerMutation(t *testing.T) {
	src := `var xs: Array<i32> = new Array<i32>(0)

def f() {
    xs.push(1)
}
`
	ds := topWrites(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeTopLevelWrite {
		t.Fatalf("diagnostics = %s", diag.FormatAll(ds))
	}
}

func TestTopLevelWriteRejectsFieldWrite(t *testing.T) {
	src := `struct G {
    n: i32
}

var g: G = G { n: 0 }

def f() {
    g.n = 1
}
`
	ds := topWrites(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeTopLevelWrite {
		t.Fatalf("diagnostics = %s", diag.FormatAll(ds))
	}
}

func TestTopLevelWriteRejectsMutArgument(t *testing.T) {
	src := `var counter: i32 = 0

def set(mut x: i32) {
    x = 1
}

def f() {
    set(counter)
}
`
	ds := topWrites(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeTopLevelWrite {
		t.Fatalf("diagnostics = %s", diag.FormatAll(ds))
	}
}

func TestTopLevelWriteAllowsShadowAndHandleMethod(t *testing.T) {
	src := `struct C {
    n: i32
}

var counter: i32 = 0
var c: rc<C> = new rc<C>(C { n: 0 })

def (mut x: C) bump() {
    x.n += 1
}

def f() {
    var counter: i32 = 1
    counter = 2
    c.bump()
}
`
	ds := topWrites(t, src)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
	}
}
