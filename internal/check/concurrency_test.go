package check

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func runConcurrency(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	file, errs := parser.Parse("test.will", []byte(src))
	if len(errs) != 0 {
		t.Fatalf("parse: %v", errs)
	}
	return Concurrency(file)
}

func hasCode(ds []diag.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestSpawnOperandMustBeOneCall(t *testing.T) {
	// K0005 is reported by the parser at the spawn itself; check sees only
	// files that parsed.
	_, ds := parser.Parse("test.will", []byte(`def f() {
    region {
        spawn helper
    }
}

def helper() {}
`))
	if !hasCode(ds, "K0005") {
		t.Fatalf("expected K0005 for a bare function spawn, got %v", ds)
	}
	_, ds = parser.Parse("test.will", []byte(`def f() {
    region {
        spawn helper()
    }
}

def helper() {}
`))
	if hasCode(ds, "K0005") {
		t.Fatalf("a call-shaped spawn must parse clean: %v", ds)
	}
}

func TestRegionRejectsUnawaitedTask(t *testing.T) {
	ds := runConcurrency(t, `async def zero() -> i32 {
    return 0
}

async def f() {
    region {
        var task: Task<i32> = spawn zero()
        _ = task
    }
}
`)
	if !hasCode(ds, diag.CodeTaskNotAwaited) {
		t.Fatalf("expected K0015, got:\n%s", diag.FormatAll(ds))
	}
}

func TestRegionRejectsEarlyReturnWithTask(t *testing.T) {
	ds := runConcurrency(t, `async def zero() -> i32 {
    return 0
}

async def f() -> i32 {
    region {
        var task: Task<i32> = spawn zero()
        if True {
            return 1
        }
        _ = await task
    }
    return 0
}
`)
	if !hasCode(ds, diag.CodeTaskNotAwaited) {
		t.Fatalf("expected K0015 for early return, got:\n%s", diag.FormatAll(ds))
	}
}

func TestRegionAcceptsAwaitedTask(t *testing.T) {
	ds := runConcurrency(t, `async def zero() -> i32 {
    return 0
}

async def f() {
    region {
        var task: Task<i32> = spawn zero()
        _ = await task
    }
}
`)
	if hasCode(ds, diag.CodeTaskNotAwaited) {
		t.Fatalf("awaited task was rejected:\n%s", diag.FormatAll(ds))
	}
}

func TestTaskHandleCannotEscapeRegion(t *testing.T) {
	// The assignment carries the handle into an outer binding, so the use
	// after the region escapes. A container push needs the parameter modes
	// and is the type-level slice of this rule.
	ds := runConcurrency(t, `def f() {
    var keep: Task<i32> = spawn zero()
    region {
        var t: Task<i32> = spawn work(1)
        keep = t
    }
    print(await keep!)
}

def zero() -> i32 { return 0 }
def work(x: i32) -> i32 { return x }
`)
	if !hasCode(ds, "K0003") {
		t.Fatalf("expected K0003 for the handle carried outside, got %v", ds)
	}
}

func TestTaskHandleInsideRegionIsFine(t *testing.T) {
	ds := runConcurrency(t, `def f() {
    region {
        var t: Task<i32> = spawn work(1)
        print(await t!)
    }
}

def work(x: i32) -> i32 { return x }
`)
	if hasCode(ds, "K0003") {
		t.Fatalf("a handle joined inside its region is fine: %v", ds)
	}
}
func TestFunctionRejectsDroppedTask(t *testing.T) {
	ds := runConcurrency(t, `async def zero() -> i32 {
    return 0
}

async def f() {
    var task: Task<i32> = spawn zero()
    _ = task
}
`)
	if !hasCode(ds, diag.CodeTaskNotAwaited) {
		t.Fatalf("expected K0015 for a dropped task, got:\n%s", diag.FormatAll(ds))
	}
}

func TestFunctionAcceptsReturnedTask(t *testing.T) {
	ds := runConcurrency(t, `async def zero() -> i32 {
    return 0
}

async def make() -> Task<i32> {
    var task: Task<i32> = spawn zero()
    return task
}
`)
	if hasCode(ds, diag.CodeTaskNotAwaited) {
		t.Fatalf("a returned task must be accepted: %v", ds)
	}
}

func TestFunctionAcceptsHandedOffTask(t *testing.T) {
	ds := runConcurrency(t, `async def zero() -> i32 {
    return 0
}

async def use(t: Task<i32>) -> i32 {
    return await t
}

async def f() -> i32 {
    var task: Task<i32> = spawn zero()
    return await use(task)
}
`)
	if hasCode(ds, diag.CodeTaskNotAwaited) {
		t.Fatalf("a task handed to a call must be accepted: %v", ds)
	}
}
