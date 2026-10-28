package gen

import (
	"strings"
	"testing"
)

// TestSpawnSyncMethodStartsOneJob checks that a sync method call becomes a job
// with the receiver carried in the frame.
func TestSpawnSyncMethodStartsOneJob(t *testing.T) {
	src := `struct Counter {
    n: i32
}

def (c: ref Counter) get() -> i32 {
    return c.n
}

async def main() -> i32 {
    var c: Counter = Counter { n: 7 }
    region {
        var task: Task<i32> = spawn c.get()
        return await task!
    }
    return 0
}
`
	doc := emitWillc(t, src)
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, "call ptr @willrt_job_new") {
		t.Errorf("sync method spawn did not use a job:\n%s", text)
	}
	if !strings.Contains(text, `call i32 @"example.com/app.Counter.get"(ptr`) {
		t.Errorf("job step did not pass the receiver:\n%s", text)
	}
}

// TestSpawnAsyncMethodStartsOneTask checks that spawn accepts a direct method
// call and that await joins the constructor's task handle.
func TestSpawnAsyncMethodStartsOneTask(t *testing.T) {
	src := `struct Counter {
    n: i32
}

async def (c: ref Counter) get() -> i32 {
    return c.n
}

async def main() -> i32 {
    var c: Counter = Counter { n: 7 }
    region {
        var task: Task<i32> = spawn c.get()
        return await task!
    }
    return 0
}
`
	doc := emitWillc(t, src)
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, `@"example.com/app.Counter.get.task"`) {
		t.Errorf("spawn did not use the method task constructor:\n%s", text)
	}
}
