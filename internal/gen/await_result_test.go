package gen

import (
	"strings"
	"testing"
)

// TestAwaitScalarArithmeticLowers checks that a direct async call awaiting a
// scalar can feed an arithmetic expression. The await type must be the call
// result itself, not an assumed Result wrapper.
func TestAwaitScalarArithmeticLowers(t *testing.T) {
	src := `async def child() -> i32 {
    return 1
}

async def main() -> i32 {
    return await child() + 1
}
`
	doc := emitWillc(t, src)
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, "call i32 @willrt_task_try_join(") {
		t.Errorf("IR lacks the non-blocking task join:\n%s", text)
	}
	if !strings.Contains(text, "llvm.sadd.with.overflow.i32") {
		t.Errorf("IR lacks the awaited scalar addition:\n%s", text)
	}
}
