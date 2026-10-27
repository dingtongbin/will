package check

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/types"
)

func crossDiagnostics(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %v", ds)
	}
	res, ds := resolve.Resolve(f)
	if len(ds) != 0 {
		t.Fatalf("resolve: %v", ds)
	}
	checked, ds := types.CheckWithFull(f, res, nil)
	if len(ds) != 0 {
		t.Fatalf("types: %v", ds)
	}
	return CrossTask(f, checked)
}

func TestSpawnRejectsRcCapture(t *testing.T) {
	src := `struct C { n: i32 }

def use(c: rc<C>) -> i32 {
    return c.n
}

def f(c: rc<C>) {
    region {
        var t: Task<i32> = spawn use(c)
        _ = t
    }
}
`
	ds := crossDiagnostics(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeCrossTask {
		t.Fatalf("diagnostics = %s, want one T0016", diag.FormatAll(ds))
	}
}

func TestSpawnRejectsClosureWithRcCapture(t *testing.T) {
	src := `struct C { n: i32 }

def f(c: rc<C>) {
    region {
        var work = func() -> i32 {
            return c.n
        }
        var t: Task<i32> = spawn work()
        _ = t
    }
}
`
	ds := crossDiagnostics(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeCrossTask {
		t.Fatalf("diagnostics = %s, want one T0016", diag.FormatAll(ds))
	}
}

func TestSpawnAllowsArcCapture(t *testing.T) {
	src := `struct C { n: i32 }

def use(c: arc<C>) -> i32 {
    return c.n
}

def f(c: arc<C>) {
    region {
        var t: Task<i32> = spawn use(c)
        _ = t
    }
}
`
	if ds := crossDiagnostics(t, src); len(ds) != 0 {
		t.Fatalf("arc capture was rejected:\n%s", diag.FormatAll(ds))
	}
}
