package check

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func cycles(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	return Cycles(f)
}

func TestCycleSelfEdgeIsAnError(t *testing.T) {
	ds := cycles(t, "struct Node {\n    next: rc<Node>\n}\n")
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d:\n%s", len(ds), diag.FormatAll(ds))
	}
	if ds[0].Code != diag.CodeUnreleasableCycle || ds[0].Severity != diag.Error {
		t.Fatalf("diagnostic = %+v", ds[0])
	}
	if ds[0].Message != "this cycle cannot be released" {
		t.Fatalf("message = %q", ds[0].Message)
	}
	if !strings.Contains(ds[0].Help, "Node.next") {
		t.Fatalf("help = %q", ds[0].Help)
	}
}

func TestCycleTwoTypesWarns(t *testing.T) {
	ds := cycles(t, "struct A {\n    b: rc<B>\n}\n\nstruct B {\n    a: rc<A>\n}\n")
	if len(ds) != 2 {
		t.Fatalf("diagnostics = %d:\n%s", len(ds), diag.FormatAll(ds))
	}
	for _, d := range ds {
		if d.Code != diag.CodePossibleCycle || d.Severity != diag.Warning {
			t.Fatalf("diagnostic = %+v", d)
		}
		if d.Message != "this field may form a cycle" {
			t.Fatalf("message = %q", d.Message)
		}
	}
}

func TestCycleWeakEdgeBreaksIt(t *testing.T) {
	ds := cycles(t, "struct A {\n    b: weak<B>\n}\n\nstruct B {\n    a: rc<A>\n}\n")
	if len(ds) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
}

func TestCycleThroughOption(t *testing.T) {
	ds := cycles(t, "struct Node {\n    next: Option<rc<Node>>\n}\n")
	if len(ds) != 1 || ds[0].Code != diag.CodeUnreleasableCycle {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
}
