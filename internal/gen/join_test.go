package gen

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

// TestJoinErrorSlotHoldsTheStatus checks that the entry-block slot for a join
// reserves room for the status word. The compiler writes that word after the
// error, so an undersized slot lets the store run past it and corrupt whatever
// sits next to the slot.
func TestJoinErrorSlotHoldsTheStatus(t *testing.T) {
	src := `def Work() -> i64 {
    return 1234567890123i64
}

def Run(task: Task<i64>) -> i64 {
    return task.Join()!
}

def main() -> i32 {
    region {
        var t: Task<i64> = spawn Work()
        var v: i64 = t.Join()!
        print("v=", v, "\n")
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
	found := false
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "join.err.") && strings.Contains(line, "= alloca") {
			found = true
			if !strings.HasSuffix(strings.TrimSpace(line), "alloca %willrt.task_error") {
				t.Errorf("join error slot is too small: %q", line)
			}
		}
	}
	if !found {
		t.Fatalf("IR has no join error slot:\n%s", text)
	}
}

// TestJoinResultCarriesTaskError checks that Join in value position builds a
// Result with the task error table, not just the unwrap panic path.
func TestJoinResultCarriesTaskError(t *testing.T) {
	src := `def Work() -> i32 {
    return 7
}

def Run(task: Task<i32>) -> Result<i32, TaskError> {
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
		"call i32 @willrt_task_try_join",
		"join.result",
		"will.task.error.table",
		"@will.task.error.message",
		"define internal ptr @will.task.error.clone",
		"define internal void @will.task.error.drop",
		"task.error.copy",
		"call void @willrt_free_str",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
