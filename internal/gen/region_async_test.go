package gen

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

// TestAsyncRegionParks checks that a region in an async function exits through
// the try form and records a wait, instead of blocking the lane in the sync
// barrier.
func TestAsyncRegionParks(t *testing.T) {
	src := `def work() -> i32 {
    return 1
}

async def main() -> i32 {
    region {
        var a: Task<i32> = spawn work()
        _ = await a!
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
		"call i32 @willrt_region_exit_try()",
		"call void @willrt_task_mark_wait()",
		"declare i32 @willrt_region_exit_try()",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
