package gen

import (
	"strings"
	"testing"
)

// TestAsyncFileCallBecomesStateMachine checks the compiler lowering, not the
// library shim: a function whose only wait is one async file call must expose
// a step function that starts the operation and parks until the pool reports
// completion.
func TestAsyncCallerWaitsOnAChildTask(t *testing.T) {
	src := "import c \"fs.h\" {\n" +
		"    fast willrt_fs_async_read_file\n" +
		"}\n" +
		"func Inner(path: str) -> Result<i32, i32> {\n" +
		"    var out: Array<u8>\n" +
		"    var status: i32 = c.willrt_fs_async_read_file(raw(path), raw(out))\n" +
		"    if status != 0 { return Err(1) }\n" +
		"    return Ok(1)\n" +
		"}\n" +
		"func Outer(path: str) -> Result<i32, i32> {\n" +
		"    var n: i32 = Inner(path)?\n" +
		"    return Ok(n + 1)\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.Outer.sm"(ptr %frame) {`,
		`call ptr @"example.com/app.Inner.task"(ptr %`,
		`call i32 @willrt_task_try_join(ptr %`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestSyncWaitUsesTheTaskNode checks that a sync acquisition in a task parks
// on the embedded sync node. The default poll would treat the returned node as
// a blocking-pool job.
func TestSyncWaitUsesTheTaskNode(t *testing.T) {
	src := "import c \"sync.h\" {\n" +
		"    fast willrt_sync_sema_acquire_async\n" +
		"}\n" +
		"func Take(s: raw) -> i32 {\n" +
		"    var status: i32 = c.willrt_sync_sema_acquire_async(s)\n" +
		"    return status\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call ptr @willrt_sync_sema_acquire_async_start(ptr`,
		`call i32 @willrt_sync_wait_poll(ptr`,
		`call i32 @willrt_sync_wait_finish(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestAsyncSleepUsesTheTimerDeadline(t *testing.T) {
	src := "import c \"clock.h\" {\n" +
		"    fast willrt_task_sleep_async\n" +
		"}\n" +
		"func Nap(ns: i64) -> i32 {\n" +
		"    var status: i32 = c.willrt_task_sleep_async(ns)\n" +
		"    return status\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call ptr @willrt_task_sleep_async_start(i64 %`,
		`call i64 @willrt_task_sleep_deadline(ptr %`,
		`call void @willrt_task_wait_until(i64 %`,
		`call i32 @willrt_task_sleep_poll(ptr %`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestTwoDirectAsyncCallsGetTwoWaitStates(t *testing.T) {
	src := "import c \"fs.h\" {\n" +
		"    fast willrt_fs_async_read_file\n" +
		"}\n" +
		"func Two(path: str) -> i32 {\n" +
		"    var out: Array<u8>\n" +
		"    var first: i32 = c.willrt_fs_async_read_file(raw(path), raw(out))\n" +
		"    var second: i32 = c.willrt_fs_async_read_file(raw(path), raw(out))\n" +
		"    return first + second\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`switch i32 %`,
		`i32 1, label %sm.wait.0`,
		`i32 2, label %sm.wait.1`,
		`sm.wait.1:`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestAsyncFileCallBecomesStateMachine(t *testing.T) {
	src := "import c \"fs.h\" {\n" +
		"    fast willrt_fs_async_read_file\n" +
		"}\n" +
		"func Load(path: str) -> i32 {\n" +
		"    var out: Array<u8>\n" +
		"    var status: i32 = c.willrt_fs_async_read_file(raw(path), raw(out))\n" +
		"    return status\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.Load.sm"(ptr %frame) {`,
		`call ptr @willrt_fs_async_read_file_start(ptr %`,
		`call i32 @willrt_io_poll(ptr %`,
		`call i32 @willrt_io_finish(ptr %`,
		`call i64 @willrt_io_deadline(ptr %`,
		`call void @willrt_task_mark_io()`,
		`call void @willrt_task_mark_wait()`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
