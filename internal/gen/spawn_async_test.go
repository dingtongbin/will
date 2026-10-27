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

async def Read(text: str) -> i32 {
    var status: i32 = c.willrt_task_sleep_async(1)
    if status != 0 { return 1 }
    return i32(text.Len())
}

async def main() -> i32 {
    var result: i32 = 0
    region {
        var task: Task<i32> = spawn Read("hello".Clone())
        result = await task.Join()!
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
		`define ptr @"example.com/app.Read.task"(ptr %arg0, i32 %start, i32 %owns)`,
		`define void @"example.com/app.Read.drop"(ptr %frame)`,
		`define void @"example.com/app.Read.drop.own"(ptr %frame)`,
		`icmp ne i32 %owns, 0`,
		`call void @willrt_free_str(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestAsyncSpawnPlaceLowers passes the placement discriminator to the async
// constructor: -1 is auto, -2 is the current lane, and a declared lane index is
// used for an explicit lane. The constructor itself chooses the runtime start
// entry from that value.
func TestAsyncSpawnPlaceLowers(t *testing.T) {
	src := `lane Ui struct UiMarker {
    id: i32
}

async def Fetch(value: i32) -> i32 {
    return value
}

def main() -> i32 {
    region {
        var a: Task<i32> = spawn Fetch(1)
        var b: Task<i32> = spawn here Fetch(2)
        var c: Task<i32> = spawn on Ui Fetch(3)
        _ = a
        _ = b
        _ = c
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
		`@"example.com/app.Fetch.task"(i32 1, i32 -1, i32 1)`,
		`@"example.com/app.Fetch.task"(i32 2, i32 -2, i32 1)`,
		`@"example.com/app.Fetch.task"(i32 3, i32 0, i32 1)`,
		`call void @willrt_task_run_auto(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
