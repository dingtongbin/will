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

// TestMutableScalarABI pins the pointer ABI for a mutable scalar in both an
// ordinary call and an async task constructor.
func TestMutableScalarABI(t *testing.T) {
	src := "func Inc(mut value: i32) -> i32 {\n    value += 1\n    return value\n}\n\n" +
		"async func Bump(mut value: i32) -> i32 {\n    value += 1\n    return value\n}\n\n" +
		"async func Use() -> i32 {\n" +
		"    var value: i32 = 1\n" +
		"    var first: i32 = Inc(value)\n" +
		"    var second: i32 = await Bump(value)\n" +
		"    return first + second\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.Inc"(ptr %value)`,
		`call i32 @"example.com/app.Inc"(ptr`,
		`define ptr @"example.com/app.Bump.task"(ptr %arg0, i32 %start, i32 %owns)`,
		`call ptr @"example.com/app.Bump.task"(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
