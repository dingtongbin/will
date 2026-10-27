package gen

import (
	"strings"
	"testing"
)

// TestGeneralFrameDropReleasesOwnedLocals checks that the raw general frame
// initializes and releases local owners. A channel wait keeps the function on
// the general path, so the frame cleanup cannot rely on the typed async layout.
func TestGeneralFrameDropReleasesOwnedLocals(t *testing.T) {
	src := `async def worker(ch: chan<i32>) -> i32 {
    var text: str = "hello"
    var items: Array<i32> = new Array<i32>(2)
    var got: Option<i32> = await ch.recv()
    _ = got
    return i32(text.len()) + i32(items.len())
}

async def main() -> i32 {
    var ch: chan<i32> = new chan<i32>(1)
    region {
        var task: Task<i32> = spawn worker(ch)
        _ = await ch.send(1)
        _ = await task.join()
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
		`define internal i32 @"example.com/app.worker.sm"(ptr %frame)`,
		`call ptr @willrt_task_new_drop(ptr @"example.com/app.worker.sm"`,
		`define internal void @"example.com/app.worker.drop"(ptr %frame) {`,
		`call void @willrt_free_str(ptr`,
		`call void @willrt_free(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestSendLoopBecomesStateMachine checks the general state-machine path: a
// ranged loop whose only wait is a channel send parks on the task wait node
// instead of blocking the lane.
func TestSendLoopBecomesStateMachine(t *testing.T) {
	src := `def produce(ch: chan<i32>, n: i32) {
    for i in Range(0, n) {
        _ = ch.send(i)
    }
}

def main() -> i32 {
    var ch: chan<i32> = new chan<i32>(1)
    region {
        var task: Task<()> = spawn produce(ch, 1)
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
		`define internal i32 @"example.com/app.produce.sm"(ptr %frame)`,
		`call i32 @willrt_chan_send_wait(ptr %`,
		`sm.op.call.1:`,
		`icmp slt i32 %`,
		`call void @willrt_task_mark_wait()`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "call i32 @willrt_chan_send(ptr") {
		t.Errorf("send loop still uses the blocking send:\n%s", text)
	}
}
