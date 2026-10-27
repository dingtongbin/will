package types

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
)

func TestTypeAliasIsTransparent(t *testing.T) {
	src := `type UserId = i64

def load(id: UserId) -> i64 {
    return id
}

def save(id: i64) -> UserId {
    return id
}
`
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("alias was not transparent:\n%s", diag.FormatAll(ds))
	}
}

func TestTypeAliasCycle(t *testing.T) {
	src := `type A = B
type B = A
`
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeTypeAliasCycle)
	if d.Message != "type alias cycle: A -> B -> A" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "break the cycle; aliases must resolve to a concrete type" {
		t.Errorf("help = %q", d.Help)
	}
}
