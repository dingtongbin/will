package resolve

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func resolveSource(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse diagnostics:\n%s", diag.FormatAll(ds))
	}
	_, rds := Resolve(f)
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
	ds := resolveSource(t, "func F() {\n    var total: i32 = 0\n    _ = total + tota\n}\n")
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
	ds := resolveSource(t, "func F() {\n    var i32: i32 = 0\n    _ = i32\n}\n")
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
func F() {}
`
	ds := resolveSource(t, src)
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
func F() {
    _ = http.get
    _ = http.Get
}
`
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodePrivateName)
	if d.Message != "private name used outside its package" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "it starts with a lowercase letter, so it is private" {
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
func F() {}
`
	ds := resolveSource(t, src)
	find(t, ds, diag.CodeUnusedImport)
}

func TestUnusedLocal(t *testing.T) {
	src := `func F(a: i32) {
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
	src := `func F() {
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
	ds := resolveSource(t, "func F() {}\nfunc F() {}\n")
	d := find(t, ds, diag.CodeDuplicateName)
	if d.Message != "duplicate top-level name F" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Pos.Line != 2 || d.Pos.Col != 6 {
		t.Errorf("position = %d:%d, want 2:6", d.Pos.Line, d.Pos.Col)
	}
}

func TestDuplicateImportName(t *testing.T) {
	src := "import \"a/http\"\nimport \"b/http\"\nfunc F() {}\n"
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
	src := "struct P {\n    x: i32\n}\n\nderive Foo for P\n"
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeUndefinedName)
	if d.Pos.Line != 5 {
		t.Errorf("position line = %d, want 5", d.Pos.Line)
	}
	if d.Help != "derive takes Json or Show" {
		t.Errorf("help = %q, want derive takes Json or Show", d.Help)
	}
}

func TestDeriveBuiltinConstraintIsNotAGenerator(t *testing.T) {
	src := "struct P {\n    x: i32\n}\n\nderive Equal for P\n"
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeUndefinedName)
	if d.Help != "derive takes Json or Show" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestDuplicateDerive(t *testing.T) {
	src := "struct P {\n    x: i32\n}\n\nderive Show for P\nderive Show for P\n"
	ds := resolveSource(t, src)
	d := find(t, ds, diag.CodeDuplicateDerive)
	if d.Message != "duplicate derive for P" || d.Help != "remove the earlier one" {
		t.Errorf("diagnostic = %+v", d)
	}
	if d.Pos.Line != 6 {
		t.Errorf("position line = %d, want 6", d.Pos.Line)
	}
}

func TestDuplicateImportLine(t *testing.T) {
	src := "import \"std/net/http\"\nimport \"std/net/http\"\nfunc F() {}\n"
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
func F() {
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
func F() {
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
