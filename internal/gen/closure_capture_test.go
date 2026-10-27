package gen

import (
	"strings"
	"testing"
)

// TestClosureMovesOwnedLocalCapture checks that a direct local capture is
// moved into the environment: the source slot is cleared, so the frame release
// and the environment drop cannot both own the buffer.
func TestClosureMovesOwnedLocalCapture(t *testing.T) {
	src := `def make() -> func() -> usize {
    var text: str = "hello"
    return func() -> usize {
        return text.len()
    }
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call ptr @willrt_closure_new(i64`,
		`define void @"example.com/app.lambda.share.1"(ptr %env)`,
		`store { ptr, i64 } zeroinitializer, ptr %v.text`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
