package types

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
)

func TestAsyncRejectsBlockingCpuSync(t *testing.T) {
	src := `struct Mutex {
    value: i32
}

def (m: ref Mutex) lock_blocking() -> i32 {
    return m.value
}

async def f(m: ref Mutex) {
    var v: i32 = m.lock_blocking()
    _ = v
}
`
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeBlockingSyncInAsync)
	if d.Message != "a blocking CPU sync call cannot run in an async function" {
		t.Fatalf("message = %q", d.Message)
	}
}

func TestCpuDefAcceptsBlockingCpuSync(t *testing.T) {
	src := `struct Mutex {
    value: i32
}

def (m: ref Mutex) lock_blocking() -> i32 {
    return m.value
}

def f(m: ref Mutex) -> i32 {
    return m.lock_blocking()
}
`
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("a CPU def was rejected:\n%s", diag.FormatAll(ds))
	}
}
