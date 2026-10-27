package gen

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

// TestAsyncSpawnOwnsByValueParameters checks the ownership discriminator on a
// spawned async frame: the frame gets a drop variant that releases its string
// parameter, while await keeps the borrowed variant.
func TestAsyncSpawnOwnsByValueParameters(t *testing.T) {
	src := `import c "clock.h" {
    fast willrt_task_sleep_async
}

async def read(text: str) -> i32 {
    var status: i32 = c.willrt_task_sleep_async(1)
    if status != 0 { return 1 }
    return i32(text.len())
}

async def main() -> i32 {
    var result: i32 = 0
    region {
        var task: Task<i32> = spawn read("hello".clone())
        result = await task!
    }
    return result
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define ptr @"example.com/app.read.task"(ptr %arg0, i32 %start, i32 %owns)`,
		`define void @"example.com/app.read.drop"(ptr %frame)`,
		`define void @"example.com/app.read.drop.own"(ptr %frame)`,
		`icmp ne i32 %owns, 0`,
		`call void @willrt_free_str(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestAsyncSpawnLowersToAutoTask passes -1 as the placement discriminator:
// every spawn is one task, and the runtime chooses its lane.
func TestAsyncSpawnLowersToAutoTask(t *testing.T) {
	src := `async def fetch(value: i32) -> i32 {
    return value
}

def main() -> i32 {
    region {
        var a: Task<i32> = spawn fetch(1)
        _ = await a
    }
    return 0
}
`
	doc := emitWillc(t, src)
	ir, err := EmitPackageModule([]*willc.Doc{doc}, "example.com/app", "main", "example.com/app", nil, false)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		`@"example.com/app.fetch.task"(i32 1, i32 -1, i32 1)`,
		`call void @willrt_task_run_auto(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
