package gen

import (
	"strings"
	"testing"
)

// TestPanicPathSkipsDefers checks that a panic statement terminates its path
// before the block's deferred calls are lowered.
func TestPanicPathSkipsDefers(t *testing.T) {
	src := `def f() {
    defer print("deferred")
    panic("boom")
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	if !strings.Contains(text, "call void @willrt_panic_n") {
		t.Fatalf("IR lacks the panic call:\n%s", text)
	}
	if strings.Contains(text, `call void @"example.com/app.defer.0"`) {
		t.Fatalf("panic path calls the defer thunk:\n%s", text)
	}
}
