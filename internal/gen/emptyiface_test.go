package gen

import (
	"strings"
	"testing"
)

// TestEmitEmptyInterface checks the anonymous interface type, its table, and a
// concrete downcast from a value that could hold any type.
func TestEmitEmptyInterface(t *testing.T) {
	src := "def check(value: interface {}) -> bool {\n    return value.is<i32>()\n}\n\n" +
		"def number(value: interface {}) -> Option<i32> {\n    return value.as<i32>()\n}\n\n" +
		"def use() -> i32 {\n" +
		"    var value: interface {} = 7\n" +
		"    if not check(value) {\n        return 0\n    }\n" +
		"    var num: Option<i32> = number(value)\n" +
		"    return match num {\n        Some(v) => v\n        None => 0\n    }\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"%interface.empty = type { ptr, ptr }",
		"will.type.i32",
		"getelementptr inbounds [3 x ptr], ptr",
		"cast.check.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
