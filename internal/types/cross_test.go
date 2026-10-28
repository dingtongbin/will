package types

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
)

func TestChannelAndTaskPayloadsMustBeSend(t *testing.T) {
	src := `struct Local { n: i64 }
struct HoldsRc { value: rc<Local> }

def f(ch: chan<HoldsRc>, task: Task<HoldsRc>) {
    _ = ch
    _ = task
}
`
	ds := checkWithResolve(t, src)
	findCode(t, ds, diag.CodeCrossTask)
}

func TestGenericChannelPayloadIsDeferred(t *testing.T) {
	src := `def f<T>(ch: chan<T>) {
    _ = ch
}
`
	ds := checkWithResolve(t, src)
	if len(ds) != 0 {
		t.Fatalf("generic channel was rejected:\n%s", diag.FormatAll(ds))
	}
}
