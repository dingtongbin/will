package gen

import (
	"strings"
	"testing"
)

// TestGeneralFrameDropsChildResult checks that a borrowed child result is
// released after its statement and that the same slot is in the frame drop for
// an abnormal exit.
func TestGeneralFrameDropsChildResult(t *testing.T) {
	src := `import c "clock.h" {
    fast willrt_task_sleep_async
}

async def text() -> str {
    var status: i32 = c.willrt_task_sleep_async(1)
    if status != 0 {
        return ""
    }
    return "hello"
}

async def parent() -> i32 {
    var n: i32 = i32((await text()).len())
    return n
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.parent.sm"(ptr %frame)`,
		`define void @"example.com/app.parent.drop"(ptr %frame)`,
		`call void @willrt_free_str(ptr`,
		`store { ptr, i64 } zeroinitializer, ptr %v.sm.call.out.`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestAsyncFrameDropsChildResult checks the typed async result field: it is
// part of the frame drop and is cleared when the after statement moves it.
func TestAsyncFrameDropsChildResult(t *testing.T) {
	src := `import c "clock.h" {
    fast willrt_task_sleep_async
}

async def text() -> str {
    var status: i32 = c.willrt_task_sleep_async(1)
    if status != 0 {
        return ""
    }
    return "hello"
}

async def parent() -> i32 {
    var text: str = await text()
    return i32(text.len())
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.parent.sm"(ptr %frame)`,
		`define void @"example.com/app.parent.drop"(ptr %frame)`,
		`call void @willrt_free_str(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
