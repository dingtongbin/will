package gen

import (
	"strings"
	"testing"
)

// TestAsyncMutParametersKeepTheCallerPlace pins the borrow form of an async
// mutable parameter. The task frame holds the caller's pointer, so a suspend
// cannot silently change writes into a frame copy that the caller never sees.
func TestAsyncMutParametersKeepTheCallerPlace(t *testing.T) {
	src := "struct Counter {\n    n: i32\n}\n\n" +
		"async func (mut c: Counter) Add(d: i32) -> i32 {\n    c.n += d\n    return c.n\n}\n\n" +
		"async func Grow(mut xs: Array<i32>) -> i32 {\n    xs.Push(3)\n    return i32(xs.Len())\n}\n\n" +
		"async func Use() -> i32 {\n" +
		"    var c: Counter = Counter { n: 0 }\n" +
		"    var added: i32 = await c.Add(4)\n" +
		"    var xs: Array<i32> = new Array<i32>(0)\n" +
		"    var grown: i32 = await Grow(xs)\n" +
		"    return c.n + i32(xs.Len()) + added + grown\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define ptr @"example.com/app.Counter.Add.task"`,
		`define ptr @"example.com/app.Grow.task"`,
		"store ptr %arg0, ptr",
		"load ptr, ptr %v.sm.param.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
