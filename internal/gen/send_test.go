package gen

import (
	"strings"
	"testing"
)

// TestSendResultCarriesClosedFault checks that Send is a Result value, not a
// fire-and-forget statement, when it is consumed with ?.
// TestDiscardedSendDefinesTheErrorInterface checks that a module which only
// discards Send's Result still defines the predeclared Error type. No source
// field names it, so the document-level check cannot see it, but the send
// result's layout mentions the interface.
func TestDiscardedSendDefinesTheErrorInterface(t *testing.T) {
	src := `def Put(ch: chan<i32>) {
    _ = ch.Send(1)
}

def main() -> i32 {
    return 0
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	if !strings.Contains(text, "%interface.Error = type { ptr, ptr }") {
		t.Errorf("IR mentions the interface without defining it:\n%s", text)
	}
}

func TestSendResultCarriesClosedFault(t *testing.T) {
	src := `def Put(ch: chan<i32>) -> Result<(), Error> {
    ch.Send(1)?
    return Ok(())
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		"call i32 @willrt_chan_send_wait",
		"send.ok.1:",
		"send.err.1:",
		"@will.error.data.4",
		"define internal { ptr, i64 } @will.error.message.4",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
