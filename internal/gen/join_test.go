package gen

import (
	"strings"
	"testing"
)

// TestJoinResultCarriesTaskError checks that Join in value position builds a
// Result with the task error table, not just the unwrap panic path.
func TestJoinResultCarriesTaskError(t *testing.T) {
	src := `func Work() -> i32 {
    return 7
}

func Run(task: Task<i32>) -> Result<i32, TaskError> {
    var value: i32 = task.Join()?
    return Ok(value)
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		"call i32 @willrt_join",
		"join.result",
		"will.task.error.table",
		"@will.task.error.message",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
