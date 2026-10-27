package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

func onlyDiagnostic(t *testing.T, ds []diag.Diagnostic) diag.Diagnostic {
	t.Helper()
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want 1:\n%s", len(ds), diag.FormatAll(ds))
	}
	return ds[0]
}

func TestKeywordAsNameDiagnostic(t *testing.T) {
	_, ds := Parse("test.will", []byte("def f() {\n    var if: i32 = 1\n}\n"))
	d := onlyDiagnostic(t, ds)
	if d.Code != diag.CodeKeywordName || d.Message != diag.KeywordName.Text || d.Help != diag.KeywordName.Help {
		t.Fatalf("diagnostic = %+v", d)
	}
	if d.Pos.Line != 2 || d.Pos.Col != 9 {
		t.Errorf("position = %d:%d, want 2:9", d.Pos.Line, d.Pos.Col)
	}
}

func TestMissingBraceDiagnostic(t *testing.T) {
	_, ds := Parse("test.will", []byte("def f() {\n"))
	d := onlyDiagnostic(t, ds)
	if d.Code != diag.CodeUnexpectedToken || d.Message != "unexpected token" || d.Help != "expected }" {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestExpressionStatementDiagnostic(t *testing.T) {
	_, ds := Parse("test.will", []byte("def f() {\n    1 + 2\n    work()\n}\n"))
	d := onlyDiagnostic(t, ds)
	if d.Code != diag.CodeUnexpectedToken || d.Help != "expected a call or spawn" {
		t.Fatalf("diagnostic = %+v", d)
	}
	if d.Pos.Line != 2 || d.Pos.Col != 5 {
		t.Errorf("position = %d:%d, want 2:5", d.Pos.Line, d.Pos.Col)
	}
}

func TestTypeArgumentDiagnostic(t *testing.T) {
	_, ds := Parse("test.will", []byte("def f() {\n    var x: i32 = call<,>(1)\n}\n"))
	d := onlyDiagnostic(t, ds)
	if d.Code != diag.CodeTypeArgs || d.Message != diag.TypeArguments.Text || d.Help != diag.TypeArguments.Help {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestLexicalErrorsSkipParsing(t *testing.T) {
	f, ds := Parse("test.will", []byte("func f() {\n    ;\n}\n"))
	if f != nil {
		t.Error("file is not nil for a file with lexical errors")
	}
	d := onlyDiagnostic(t, ds)
	if d.Code != diag.CodeBadSymbol {
		t.Fatalf("diagnostic = %+v, want L0006", d)
	}
}

func TestDeferNeedsCall(t *testing.T) {
	_, ds := Parse("test.will", []byte("def f() {\n    defer 1\n}\n"))
	d := onlyDiagnostic(t, ds)
	if d.Code != diag.CodeUnexpectedToken || d.Help != "expected a call" {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestImportAfterDeclaration(t *testing.T) {
	_, ds := Parse("test.will", []byte("def f() {}\nimport \"std/bytes\"\n"))
	d := onlyDiagnostic(t, ds)
	if d.Code != diag.CodeUnexpectedToken || d.Help != "expected a declaration" {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestEmptyFile(t *testing.T) {
	f, ds := Parse("test.will", []byte(""))
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diag.FormatAll(ds))
	}
	if f == nil || len(f.Decls) != 0 || len(f.Imports) != 0 {
		t.Fatalf("file = %+v, want empty", f)
	}
}

func TestDocCommentBindsToDeclaration(t *testing.T) {
	f := parse(t, "/// first\n/// second\ndef f() {}\n")
	fn, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("declaration = %T, want func", f.Decls[0])
	}
	if fn.Doc != "first\nsecond" {
		t.Errorf("doc = %q, want %q", fn.Doc, "first\nsecond")
	}
}
