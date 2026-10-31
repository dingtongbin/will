package gen

import (
	"strings"
	"testing"
)

// TestSelectDiscardReleasesReceiveOut checks that a receive arm without a bind
// drops the received value and clears its frame slot before the arm body runs.
func TestSelectDiscardReleasesReceiveOut(t *testing.T) {
	src := `async def main() -> i32 {
    var ch: chan<str> = chan<str>(1)
    _ = await ch.send("hello".clone())
    select {
        _ = ch.recv() => {
        }
    }
    return 0
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call i32 @willrt_select`,
		`call void @willrt_free_str(ptr`,
		`store { ptr, i64 } zeroinitializer, ptr %v.sm.select.out.`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestSelectBindClearsReceiveOut checks that the bind moves the received value
// into its Option and zeroes the arm buffer instead of leaving a second owner.
func TestSelectBindClearsReceiveOut(t *testing.T) {
	src := `async def main() -> i32 {
    var ch: chan<str> = chan<str>(1)
    _ = await ch.send("hello".clone())
    var got: Option<str> = None
    select {
        x = ch.recv() => {
            got = x
        }
    }
    return match got {
        Some(s) => i32(s.len())
        None => 1
    }
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, `store { ptr, i64 } zeroinitializer, ptr %v.sm.select.out.`) {
		t.Errorf("the receive bind left the arm buffer live:\n%s", text)
	}
}
