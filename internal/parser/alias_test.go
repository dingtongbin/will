package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

func TestParseTypeAlias(t *testing.T) {
	f := parse(t, "type UserId = i64\n\ndef load(id: UserId) -> UserId {\n    return id\n}\n")
	if len(f.Decls) != 2 {
		t.Fatalf("decls = %d, want 2", len(f.Decls))
	}
	alias, ok := f.Decls[0].(*ast.TypeAliasDecl)
	if !ok {
		t.Fatalf("decl 0 = %T, want type alias", f.Decls[0])
	}
	if alias.Name != "UserId" || alias.Type == nil || alias.Type.Name != "i64" {
		t.Fatalf("alias = %+v, want UserId = i64", alias)
	}
}

func TestImportAlias(t *testing.T) {
	f := parse(t, "import h \"std/net/http\"\n\ndef f() {\n}\n")
	if len(f.Imports) != 1 || f.Imports[0].Alias != "h" || f.Imports[0].Path != "std/net/http" {
		t.Fatalf("import = %+v, want alias h", f.Imports)
	}
}

func TestTypeAliasIsTopLevelOnly(t *testing.T) {
	_, ds := Parse("test.will", []byte("def f() {\n    type UserId = i64\n}\n"))
	if len(ds) != 1 || ds[0].Code != diag.CodeUnexpectedToken {
		t.Fatalf("diagnostics = %v, want one S0001", ds)
	}
}
