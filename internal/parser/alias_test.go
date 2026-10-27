package parser

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
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
