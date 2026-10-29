package gen

import (
	"strings"
	"testing"
)

func TestContainersAllocateThroughTheCollector(t *testing.T) {
	src := `def main() -> i32 {
    var a: Array<i32> = new Array<i32>(2)
    var m: Map<str, i32> = new Map<str, i32>(2)
    var s: Set<i32> = new Set<i32>(2)
    a.push(1)
    m.set("k", 1)
    s.add(1)
    return a.get(0) + match m.get("k") {
        Some(v) => v
        None => 0
    }
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`@"willrt.gc.type.container.sequence.`,
		`@"willrt.gc.type.container.entry.`,
		`call ptr @willrt_gc_alloc(i64`,
		`call ptr @willrt_gc_read_barrier_slot(ptr`,
		`call void @willrt_gc_write_barrier_untagged(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
