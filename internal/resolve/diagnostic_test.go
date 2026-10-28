package resolve

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func resolveSource(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	return resolveSourceWith(t, src, nil)
}

// resolveSourceWith resolves src with the given packages available, as a
// module run would. tables maps an import path to the member names the other
// package exports.
func resolveSourceWith(t *testing.T, src string, tables map[string][]string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse diagnostics:\n%s", diag.FormatAll(ds))
	}
	_, rds := ResolveWith(f, importTables(tables))
	return rds
}

func find(t *testing.T, ds []diag.Diagnostic, code string) diag.Diagnostic {
	t.Helper()
	for _, d := range ds {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("missing %s in:\n%s", code, diag.FormatAll(ds))
	return diag.Diagnostic{}
}

func TestUndefinedName(t *testing.T) {
	ds := resolveSource(t, "def f() {\n    var total: i32 = 0\n    _ = total + tota\n}\n")
	d := find(t, ds, diag.CodeUndefinedName)
	if d.Message != "undefined name" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "did you mean total?" {
		t.Errorf("help = %q, want did you mean total?", d.Help)
	}
	if d.Pos.Line != 3 {
		t.Errorf("position line = %d, want 3", d.Pos.Line)
	}
}

func TestShadowWarning(t *testing.T) {
	ds := resolveSource(t, "def f() {\n    var i32: i32 = 0\n    _ = i32\n}\n")
	d := find(t, ds, diag.CodeShadow)
	if d.Severity != diag.Warning {
		t.Errorf("severity = %v, want warning", d.Severity)
	}
	if d.Message != "this name shadows i32" || d.Help != "rename one of them" {
		t.Errorf("diagnostic = %+v", d)
	}
	if d.Pos.Line != 2 || d.Pos.Col != 9 {
		t.Errorf("position = %d:%d, want 2:9", d.Pos.Line, d.Pos.Col)
	}
}

func TestUnusedImport(t *testing.T) {
	src := `import "std/net/http"
import _ "std/bytes"
def f() {}
`
	ds := resolveSourceWith(t, src, map[string][]string{"std/net/http": {}, "std/bytes": {}})
	d := find(t, ds, diag.CodeUnusedImport)
	if d.Message != "unused import" || d.Help != "remove it, or write import _ for side effects only" {
		t.Errorf("diagnostic = %+v", d)
	}
	if d.Pos.Line != 1 {
		t.Errorf("position line = %d, want 1", d.Pos.Line)
	}
	if len(ds) != 1 {
		t.Errorf("diagnostics = %d, want only the unused import:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestPrivateNameOutsidePackage(t *testing.T) {
	src := `import "std/net/http"
def f() {
    _ = http.__hidden
    _ = http.visible
}
`
	ds := resolveSourceWith(t, src, map[string][]string{"std/net/http": {"visible"}})
	d := find(t, ds, diag.CodePrivateName)
	if d.Message != "private name used outside its package" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "names starting with __ are private" {
		t.Errorf("help = %q", d.Help)
	}
	if d.Pos.Line != 3 || d.Pos.Col != 14 {
		t.Errorf("position = %d:%d, want 3:14", d.Pos.Line, d.Pos.Col)
	}
	count := 0
	for _, x := range ds {
		if x.Code == diag.CodePrivateName {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("N0002 count = %d, want 1:\n%s", count, diag.FormatAll(ds))
	}
}

func TestUnusedCLink(t *testing.T) {
	src := `import c "stdio.h" {
    fast puts
}
def f() {}
`
	ds := resolveSource(t, src)
	find(t, ds, diag.CodeUnusedImport)
}

func TestUnusedLocal(t *testing.T) {
	src := `def f(a: i32) {
    var x: i32 = 1
    var y: i32 = 2
    _ = y
}
`
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeUnusedVar)
	if d.Message != "variable x is never read" {
		t.Errorf("message = %q, want variable x", d.Message)
	}
	if d.Help != "delete it, read it, or discard it with _" {
		t.Errorf("help = %q", d.Help)
	}
	if d.Pos.Line != 2 || d.Pos.Col != 9 {
		t.Errorf("position = %d:%d, want 2:9", d.Pos.Line, d.Pos.Col)
	}
	if len(ds) != 1 {
		t.Errorf("diagnostics = %d, want only x:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestUnusedLoopBinding(t *testing.T) {
	src := `def f() {
    var xs: Array<i32> = new Array<i32>(0)
    for i, x in xs {
        _ = x
    }
}
`
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeUnusedVar)
	if !strings.Contains(d.Message, " i ") {
		t.Errorf("message = %q, want the loop binding i", d.Message)
	}
}

func TestDuplicateTopLevelName(t *testing.T) {
	ds := resolveSource(t, "def f() {}\ndef f() {}\n")
	d := find(t, ds, diag.CodeDuplicateName)
	if d.Message != "duplicate top-level name f" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Pos.Line != 2 || d.Pos.Col != 5 {
		t.Errorf("position = %d:%d, want 2:5", d.Pos.Line, d.Pos.Col)
	}
}

func TestDuplicateImportName(t *testing.T) {
	src := "import \"a/http\"\nimport \"b/http\"\ndef f() {}\n"
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeDuplicateName)
	if d.Message != "duplicate top-level name http" {
		t.Errorf("message = %q", d.Message)
	}
}

func TestDuplicateVariant(t *testing.T) {
	ds := resolveSource(t, "enum E {\n    A\n    A\n}\n")
	find(t, ds, diag.CodeDuplicateName)
}

func TestDeriveNameIsOneOfTwo(t *testing.T) {
	src := "@derive(Foo)\nstruct P {\n    x: i32\n}\n"
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeBadDeriveName)
	if d.Pos.Line != 2 {
		t.Errorf("position line = %d, want 2", d.Pos.Line)
	}
	if d.Message != "cannot derive Foo for P" || d.Help != "derive takes Json or Show" {
		t.Errorf("diagnostic = %+v", d)
	}
}

func TestDeriveBuiltinConstraintIsNotAGenerator(t *testing.T) {
	src := "@derive(equal)\nstruct P {\n    x: i32\n}\n"
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeBadDeriveName)
	if d.Message != "cannot derive equal for P" || d.Help != "derive takes Json or Show" {
		t.Errorf("diagnostic = %+v", d)
	}
}

func TestUnknownImportPath(t *testing.T) {
	src := "import \"std/net/http\"\n\ndef f() {}\n"
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeUnknownImportPath)
	if d.Message != "unknown import path std/net/http" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "the path must be a module path plus directories; run will get if the dependency is not fetched yet" {
		t.Errorf("help = %q", d.Help)
	}
	if d.Pos.Line != 1 {
		t.Errorf("position line = %d, want 1", d.Pos.Line)
	}
}

func TestUnknownImportPathSuggestsTheClosest(t *testing.T) {
	src := "import \"std/net/httpx\"\n\ndef f() {}\n"
	ds := resolveSourceWith(t, src, map[string][]string{"std/net/http": {}})
	d := find(t, ds, diag.CodeUnknownImportPath)
	if d.Help != "did you mean std/net/http?" {
		t.Errorf("help = %q", d.Help)
	}
}

// TestUnknownImportSuppressesItsMembers checks cascade suppression: the root
// cause is reported and the member diagnostics inside that import are not.
func TestUnknownImportSuppressesItsMembers(t *testing.T) {
	src := "import \"std/net/http\"\n\ndef f() {\n    _ = http.get\n    _ = http.status\n}\n"
	ds := resolveSource(t, src)
	roots := 0
	for _, d := range ds {
		switch d.Code {
		case diag.CodeUnknownImportPath:
			roots++
		case diag.CodeUndefinedName, diag.CodePrivateName:
			t.Errorf("member diagnostic was not suppressed: %s", d.Code)
		}
	}
	if roots != 1 {
		t.Fatalf("unknown import reported %d times, want 1:\n%s", roots, diag.FormatAll(ds))
	}
}

func TestDuplicateDerive(t *testing.T) {
	src := "@derive(Show)\n@derive(Show)\nstruct P {\n    x: i32\n}\n"
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeDuplicateDerive)
	if d.Message != "duplicate derive for P" || d.Help != "remove the earlier one" {
		t.Errorf("diagnostic = %+v", d)
	}
	if d.Pos.Line != 3 {
		t.Errorf("position line = %d, want 3", d.Pos.Line)
	}
}

func TestDuplicateImportLine(t *testing.T) {
	src := "import \"std/net/http\"\nimport \"std/net/http\"\ndef f() {}\n"
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeDuplicateImport)
	if d.Message != "duplicate import" || d.Help != "remove the earlier one" {
		t.Errorf("diagnostic = %+v", d)
	}
	if d.Pos.Line != 2 {
		t.Errorf("position line = %d, want 2", d.Pos.Line)
	}
}

func TestTopLevelDiscardName(t *testing.T) {
	ds := resolveSource(t, "var _: i32 = 0\n")
	d := find(t, ds, diag.CodeTopLevelDiscard)
	if d.Message != "\"_\" is not a name; it is the discard binding" || d.Help != "give the declaration a name" {
		t.Errorf("diagnostic = %+v", d)
	}
	if d.Pos.Line != 1 {
		t.Errorf("position line = %d, want 1", d.Pos.Line)
	}
}

func TestCLinkNeedsTheQualifier(t *testing.T) {
	src := `import c "stdio.h" {
    fast puts
}
def f() {
    puts("hi")
}
`
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeUndefinedName)
	if d.Pos.Line != 5 {
		t.Errorf("position line = %d, want 5", d.Pos.Line)
	}
}

func TestCLinkThroughQualifier(t *testing.T) {
	src := `import c "stdio.h" {
    fast puts
}
def f() {
    c.puts("hi")
}
`
	ds := resolveSource(t, src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestTopLevelShadowsPredeclared(t *testing.T) {
	ds := resolveSource(t, "const Result: i32 = 0\n")
	d := find(t, ds, diag.CodeShadow)
	if d.Message != "this name shadows Result" {
		t.Errorf("message = %q", d.Message)
	}
}
