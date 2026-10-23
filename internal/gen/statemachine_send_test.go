package gen

import (
	"strings"
	"testing"
)

// TestSendLoopBecomesStateMachine checks the producer shape used by the MPMC
// corpus: a ranged loop whose only wait is a channel send must lower to a step
// that parks on willrt_chan_send_wait instead of blocking the lane.
func TestSendLoopBecomesStateMachine(t *testing.T) {
	src := `func Produce(ch: chan<i32>, n: i32) {
    for i in Range(0, n) {
        _ = ch.Send(i)
    }
}

func main() -> i32 {
    var ch: chan<i32> = new chan<i32>(1)
    region {
        var task: Task<()> = spawn Produce(ch, 1)
        _ = task.Join()!
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
		`define i32 @"example.com/app.Produce.sm"(ptr %frame)`,
		`call i32 @willrt_chan_send_wait(ptr %`,
		`store i32 2, ptr %`,
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
