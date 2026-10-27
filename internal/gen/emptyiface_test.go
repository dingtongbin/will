package gen

import (
	"strings"
	"testing"
)

// TestEmitEmptyInterface checks the anonymous interface type, its table, and a
// concrete downcast from a value that could hold any type.
func TestEmitEmptyInterface(t *testing.T) {
	src := "def Check(value: interface {}) -> bool {\n    return value.Is<i32>()\n}\n\n" +
		"def Number(value: interface {}) -> Option<i32> {\n    return value.As<i32>()\n}\n\n" +
		"def Use() -> i32 {\n" +
		"    var value: interface {} = 7\n" +
		"    if !Check(value) {\n        return 0\n    }\n" +
		"    var number: Option<i32> = Number(value)\n" +
		"    return match number {\n        Some(v) => v\n        None => 0\n    }\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"%interface.empty = type { ptr, ptr }",
		"will.type.i32",
		"getelementptr inbounds [4 x ptr], ptr",
		"cast.check.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
