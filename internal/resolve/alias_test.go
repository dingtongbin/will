package resolve

import "testing"

func TestTypeAliasResolvesAsType(t *testing.T) {
	res, ds := mustResolve(t, `type UserId = i64

def load(id: UserId) -> UserId {
    return id
}
`)
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want 0", len(ds))
	}
	kinds := map[string]Kind{}
	for _, sym := range res.Symbols {
		kinds[sym.Name] = sym.Kind
	}
	if kinds["UserId"] != TypeAliasKind {
		t.Fatalf("UserId kind = %v, want TypeAliasKind", kinds["UserId"])
	}
}
