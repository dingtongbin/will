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
		"async def (mut c: Counter) add(d: i32) -> i32 {\n    c.n += d\n    return c.n\n}\n\n" +
		"async def grow(mut xs: Array<i32>) -> i32 {\n    xs.push(3)\n    return i32(xs.len())\n}\n\n" +
		"async def use() -> i32 {\n" +
		"    var c: Counter = Counter { n: 0 }\n" +
		"    var added: i32 = await c.add(4)\n" +
		"    var xs: Array<i32> = new Array<i32>(0)\n" +
		"    var grown: i32 = await grow(xs)\n" +
		"    return c.n + i32(xs.len()) + added + grown\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define ptr @"example.com/app.Counter.add.task"`,
		`define ptr @"example.com/app.grow.task"`,
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
	src := "def inc(mut value: i32) -> i32 {\n    value += 1\n    return value\n}\n\n" +
		"async def bump(mut value: i32) -> i32 {\n    value += 1\n    return value\n}\n\n" +
		"async def use() -> i32 {\n" +
		"    var value: i32 = 1\n" +
		"    var first: i32 = inc(value)\n" +
		"    var second: i32 = await bump(value)\n" +
		"    return first + second\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.inc"(ptr %value)`,
		`call i32 @"example.com/app.inc"(ptr`,
		`define ptr @"example.com/app.bump.task"(ptr %arg0, i32 %start, i32 %owns)`,
		`call ptr @"example.com/app.bump.task"(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
