package gen

import (
	"strings"
	"testing"
)

// TestAsyncInterfaceVtableCallsConstructor checks that an async interface
// method table stores the task constructor, not the state-machine step, and
// that await joins the constructed task.
func TestAsyncInterfaceVtableCallsConstructor(t *testing.T) {
	src := `interface Handler {
    async def handle(value: i32) -> i32
}

struct Adder {
    base: i32
}

async def (a: ref Adder) handle(value: i32) -> i32 {
    return a.base + value
}

async def main() -> i32 {
    var a: Adder = Adder { base: 40 }
    var h: Handler = a
    var n: i32 = await h.handle(2)
    return n
}
`
	doc := emitWillc(t, src)
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`@"example.com/app.vtable.Handler.Adder" = internal constant`,
		`@"example.com/app.Adder.handle.task"`,
		`call ptr %t`,
		`call i32 @willrt_join(`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
