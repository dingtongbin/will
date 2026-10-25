package gen

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

// TestAsyncSpawnPlaceLowers passes the placement discriminator to the async
// constructor: -1 is auto, -2 is the current lane, and a declared lane index is
// used for an explicit lane. The constructor itself chooses the runtime start
// entry from that value.
func TestAsyncSpawnPlaceLowers(t *testing.T) {
	src := `lane Ui struct UiMarker {
    id: i32
}

async func Fetch(value: i32) -> i32 {
    return value
}

func main() -> i32 {
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
		`@"example.com/app.Fetch.task"(i32 1, i32 -1)`,
		`@"example.com/app.Fetch.task"(i32 2, i32 -2)`,
		`@"example.com/app.Fetch.task"(i32 3, i32 0)`,
		`call void @willrt_task_run_auto(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
