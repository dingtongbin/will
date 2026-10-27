package gen

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

// TestSpawnLowersToAutoTask checks the only spawn surface: one call, one task,
// runtime-selected placement.
func TestSpawnLowersToAutoTask(t *testing.T) {
	src := `def work() -> i32 {
    return 1
}

def main() -> i32 {
    region {
        var a: Task<i32> = spawn work()
        var result: Result<i32, TaskError> = await a
        _ = result
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
		"call void @willrt_task_run_auto(",
		"declare void @willrt_task_run_auto(",
		"call i32 @willrt_join(",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
