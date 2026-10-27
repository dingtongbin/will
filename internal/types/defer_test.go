package types

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
)

func TestDeferCannotSuspend(t *testing.T) {
	src := `async def cleanup() {
}

def f() {
    defer cleanup()
}
`
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeDeferSuspend)
	if d.Message != "defer cannot suspend" {
		t.Fatalf("message = %q", d.Message)
	}
}

func TestDeferRunsSyncCall(t *testing.T) {
	src := `def cleanup() {
}

def f() {
    defer cleanup()
}
`
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("sync defer was rejected:\n%s", diag.FormatAll(ds))
	}
}
