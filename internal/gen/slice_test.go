package gen

import (
	"strings"
	"testing"
)

// TestDefaultAndNamedArguments checks call-site defaults and named binding.
func TestDefaultAndNamedArguments(t *testing.T) {
	src := `def add(a: i32, b: i32 = 2) -> i32 {
    return a + b
}

def use() -> i32 {
    var x: i32 = add(1)
    var y: i32 = add(b = 3, a = 4)
    return x + y
}
`
	doc := emitWillc(t, src)
	if _, err := Emit(doc); err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
}

// TestSliceBoundsAndLenBuiltin checks the canonical slice spelling and the
// predeclared len call. The omitted bounds become zero and the live length.
func TestSliceBoundsAndLenBuiltin(t *testing.T) {
	src := `def main() -> i32 {
    var xs: Array<i32> = new Array<i32>(4)
    xs.push(1)
    xs.push(2)
    xs.push(3)
    xs.push(4)
    var middle: []i32 = xs[1:3]
    var prefix: []i32 = xs[:2]
    var tail: []i32 = xs[1:]
    var all: []i32 = xs[:]
    return i32(len(middle) + len(prefix) + len(tail) + len(all))
}
`
	doc := emitWillc(t, src)
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"getelementptr inbounds { ptr, i64 }, ptr",
		"sub i64",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
