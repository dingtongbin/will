package gen

import (
	"strings"
	"testing"
)

// TestPanicReleasesLiveRc checks that a panic path releases an rc local before
// leaving the task, rather than only skipping defer.
func TestPanicReleasesLiveRc(t *testing.T) {
	src := `struct C {
    n: i32
}

def f() -> i32 {
    var x: rc<C> = new rc<C>(C { n: 7 })
    var value: i32 = x.n
    panic("boom")
    return value
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	panicAt := strings.Index(text, "call void @willrt_panic_n")
	releaseAt := strings.Index(text, "call void @willrt_rc_release")
	if panicAt < 0 {
		t.Fatalf("IR lacks the panic call:\n%s", text)
	}
	if releaseAt < 0 || releaseAt > panicAt {
		t.Fatalf("rc release is missing or after panic:\n%s", text)
	}
}
