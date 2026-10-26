package check

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func valueCycles(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	return ValueCycles(f)
}

func TestValueCycleTwoHandles(t *testing.T) {
	src := `struct Node {
    next: rc<Node>
}

func F() {
    var a: rc<Node>
    var b: rc<Node>
    a.next = b
    b.next = a
}
`
	ds := valueCycles(t, src)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want one:\n%s", len(ds), diag.FormatAll(ds))
	}
	d := ds[0]
	if d.Code != diag.CodeUnreleasableCycle || d.Severity != diag.Error {
		t.Fatalf("diagnostic = %+v", d)
	}
	if d.Message != "this cycle cannot be released" {
		t.Fatalf("message = %q", d.Message)
	}
	if !strings.Contains(d.Help, "a.next") || !strings.Contains(d.Help, "b.next") {
		t.Fatalf("help = %q", d.Help)
	}
}

func TestValueCycleThroughOption(t *testing.T) {
	src := `struct Node {
    next: Option<rc<Node>>
}

func F() {
    var a: rc<Node>
    var b: rc<Node>
    a.next = Some(b)
    b.next = Some(a)
}
`
	ds := valueCycles(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeUnreleasableCycle {
		t.Fatalf("diagnostics = %s, want one M0001", diag.FormatAll(ds))
	}
}

func TestValueCycleGenericInstance(t *testing.T) {
	src := `struct Node<T> {
    next: rc<Node<T>>
}

func F() {
    var a: rc<Node<i32>>
    var b: rc<Node<i32>>
    a.next = b
    b.next = a
}
`
	ds := valueCycles(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeUnreleasableCycle {
		t.Fatalf("diagnostics = %s, want one M0001", diag.FormatAll(ds))
	}
}

func TestValueCycleClosedByLinkerCall(t *testing.T) {
	src := `struct Node {
    next: rc<Node>
}

func Link(a: rc<Node>, b: rc<Node>) {
    a.next = b
    b.next = a
}

func F() {
    var x: rc<Node>
    var y: rc<Node>
    Link(x, y)
}
`
	ds := valueCycles(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeUnreleasableCycle {
		t.Fatalf("diagnostics = %s, want one M0001 at the call", diag.FormatAll(ds))
	}
	if ds[0].Pos.Line != 13 {
		t.Fatalf("position = %d, want the call line", ds[0].Pos.Line)
	}
}

func TestValueCycleLinkerAcrossFiles(t *testing.T) {
	link, ds := parser.Parse("a.will", []byte(`struct Node {
    next: rc<Node>
}

func Link(a: rc<Node>, b: rc<Node>) {
    a.next = b
    b.next = a
}
`))
	if len(ds) != 0 {
		t.Fatalf("parse a: %s", diag.FormatAll(ds))
	}
	caller, ds := parser.Parse("b.will", []byte(`func F() {
    var x: rc<Node>
    var y: rc<Node>
    Link(x, y)
}
`))
	if len(ds) != 0 {
		t.Fatalf("parse b: %s", diag.FormatAll(ds))
	}
	ds = ValueCyclesWithFiles([]*ast.File{link, caller})
	if len(ds) != 1 || ds[0].Code != diag.CodeUnreleasableCycle {
		t.Fatalf("diagnostics = %s, want one M0001", diag.FormatAll(ds))
	}
}

// TestValueCycleLinkerAcrossPackages checks the program-wide summaries: a call
// through an imported package still closes the cycle at the call site.
func TestValueCycleLinkerAcrossPackages(t *testing.T) {
	lib, ds := parser.Parse("a.will", []byte(`struct Node {
    next: rc<Node>
}

func Link(a: rc<Node>, b: rc<Node>) {
    a.next = b
    b.next = a
}
`))
	if len(ds) != 0 {
		t.Fatalf("parse a: %s", diag.FormatAll(ds))
	}
	caller, ds := parser.Parse("b.will", []byte(`import "example.com/a"

func F() {
    var x: rc<a.Node>
    var y: rc<a.Node>
    a.Link(x, y)
}
`))
	if len(ds) != 0 {
		t.Fatalf("parse b: %s", diag.FormatAll(ds))
	}
	ds = ValueCyclesProgram([]*ast.File{lib, caller}, map[*ast.File]string{
		lib:    "example.com/a",
		caller: "example.com/b",
	})
	if len(ds) != 1 || ds[0].Code != diag.CodeUnreleasableCycle {
		t.Fatalf("diagnostics = %s, want one M0001", diag.FormatAll(ds))
	}
}

// TestValueCycleMethodAcrossPackages checks a mut method linker declared in one
// package and called on an imported receiver type.
func TestValueCycleMethodAcrossPackages(t *testing.T) {
	lib, ds := parser.Parse("a.will", []byte(`struct Node {
    next: rc<Node>
}

func (a: Node) Link(b: rc<Node>, c: rc<Node>) {
    b.next = c
    c.next = b
}
`))
	if len(ds) != 0 {
		t.Fatalf("parse a: %s", diag.FormatAll(ds))
	}
	caller, ds := parser.Parse("b.will", []byte(`import "example.com/a"

func F() {
    var root: a.Node
    var x: rc<a.Node>
    var y: rc<a.Node>
    root.Link(x, y)
}
`))
	if len(ds) != 0 {
		t.Fatalf("parse b: %s", diag.FormatAll(ds))
	}
	ds = ValueCyclesProgram([]*ast.File{lib, caller}, map[*ast.File]string{
		lib:    "example.com/a",
		caller: "example.com/b",
	})
	if len(ds) != 1 || ds[0].Code != diag.CodeUnreleasableCycle {
		t.Fatalf("diagnostics = %s, want one M0001", diag.FormatAll(ds))
	}
}

func TestValueCycleWeakEdgeDoesNotClose(t *testing.T) {
	src := `struct Node {
    next: weak<Node>
}

func F() {
    var a: rc<Node>
    var b: rc<Node>
    a.next = b
    b.next = a
}
`
	if ds := valueCycles(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestValueCycleNoEdgeIsClean(t *testing.T) {
	src := `struct Node {
    next: rc<Node>
}

func F() {
    var a: rc<Node>
    var b: rc<Node>
    a.next = b
}
`
	if ds := valueCycles(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}
