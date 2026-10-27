package gen

import (
	"strings"
	"testing"
)

// TestGeneralMachineSplitsTwoChannelOps checks that a loop may contain a
// receive and a send without a separate shape for each operation.
func TestGeneralMachineSplitsTwoChannelOps(t *testing.T) {
	src := `def stage(inp: chan<i32>, out: chan<i32>) {
    while True {
        var item: Option<i32> = inp.recv()
        match item {
            Some(value) => {
                _ = out.send(value)
            }
            None => {
                break
            }
        }
    }
}

def main() -> i32 {
    var first: chan<i32> = new chan<i32>(1)
    var second: chan<i32> = new chan<i32>(1)
    region {
        var task: Task<()> = spawn stage(first, second)
        _ = task.join()!
    }
    return 0
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		`define internal i32 @"example.com/app.stage.sm"(ptr %frame)`,
		`call i32 @willrt_chan_recv_wait(ptr %`,
		`call i32 @willrt_chan_send_wait(ptr %`,
		`sm.op.call.1:`,
		`sm.op.call.2:`,
		`sm.init:`,
		`sm.op.cont.1:`,
		`sm.op.cont.2:`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
