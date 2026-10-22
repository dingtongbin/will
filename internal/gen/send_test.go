package gen

import (
	"strings"
	"testing"
)

// TestSendResultCarriesClosedFault checks that Send is a Result value, not a
// fire-and-forget statement, when it is consumed with ?.
func TestSendResultCarriesClosedFault(t *testing.T) {
	src := `func Put(ch: chan<i32>) -> Result<(), Error> {
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
		"call i32 @willrt_chan_send",
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
