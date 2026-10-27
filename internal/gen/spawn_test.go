package gen

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

// TestSpawnSyncCallLowersToJob checks the only spawn surface: a sync call is
// one CPU job on the submitting lane's deque, and await joins its handle.
func TestSpawnSyncCallLowersToJob(t *testing.T) {
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
		"call ptr @willrt_job_new(",
		"declare ptr @willrt_job_new(",
		"call i32 @willrt_join(",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
