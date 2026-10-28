package types

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
)

func TestAwaitTaskReturnsResult(t *testing.T) {
	src := `async def f(t: Task<i32>) {
    var r: Result<i32, TaskError> = await t
    t.cancel()
    _ = r
}
`
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("await Task was rejected:\n%s", diag.FormatAll(ds))
	}
}

func TestAwaitRejectsNonTask(t *testing.T) {
	src := `async def f() {
    var n: i32 = 1
    var r = await n
    _ = r
}
`
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeAwaitNotAsync)
	if d.Message != "await needs a Task or an async call" {
		t.Fatalf("message = %q", d.Message)
	}
}
