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
	_, ds := parser.Parse("test.will", []byte(`func F() {
    region {
        spawn Helper
    }
}

func Helper() {}
`))
	if !hasCode(ds, "K0005") {
		t.Fatalf("expected K0005 for a bare function spawn, got %v", ds)
	}
	_, ds = parser.Parse("test.will", []byte(`func F() {
    region {
        spawn Helper()
    }
}

func Helper() {}
`))
	if hasCode(ds, "K0005") {
		t.Fatalf("a call-shaped spawn must parse clean: %v", ds)
	}
}

func TestTaskHandleCannotEscapeRegion(t *testing.T) {
	// The assignment carries the handle into an outer binding, so the use
	// after the region escapes. A container push needs the parameter modes
	// and is the type-level slice of this rule.
	ds := runConcurrency(t, `func F() {
    var keep: Task<i32> = spawn Zero()
    region {
        var t: Task<i32> = spawn Work(1)
        keep = t
    }
    print(keep.Join()!)
}

func Zero() -> i32 { return 0 }
func Work(x: i32) -> i32 { return x }
`)
	if !hasCode(ds, "K0003") {
		t.Fatalf("expected K0003 for the handle carried outside, got %v", ds)
	}
}

func TestTaskHandleInsideRegionIsFine(t *testing.T) {
	ds := runConcurrency(t, `func F() {
    region {
        var t: Task<i32> = spawn Work(1)
        print(t.Join()!)
    }
}

func Work(x: i32) -> i32 { return x }
`)
	if hasCode(ds, "K0003") {
		t.Fatalf("a handle joined inside its region is fine: %v", ds)
	}
}

func TestWaitInsideSuspendingFunctionWarns(t *testing.T) {
	ds := runConcurrency(t, `async func Load(ch: chan<i32>) -> Result<i32, Error> {
    var x: Option<i32> = ch.Recv()
    var v: Result<i32, Error> = wait(ParseAsync(x!))?
    return Ok(v)
}
`)
	if !hasCode(ds, "I0003") {
		t.Fatalf("expected I0003 for wait in a suspending function, got %v", ds)
	}
	for _, d := range ds {
		if d.Code == "I0003" && d.Severity != diag.Warning {
			t.Fatalf("I0003 is a warning, got %v", d.Severity)
		}
	}
}

func TestWaitInSyncFunctionIsTheBridge(t *testing.T) {
	ds := runConcurrency(t, `func Load(raw: []u8) -> Result<i32, Error> {
    var v: Result<i32, Error> = wait(ParseAsync(raw))?
    return Ok(v)
}
`)
	if hasCode(ds, "I0003") {
		t.Fatalf("wait is the bridge in a function with no other suspension: %v", ds)
	}
}
