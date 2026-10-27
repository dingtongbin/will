package check

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func rcSlots(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	own := NewOwnership([]*ast.File{f})
	return RcSlots([]*ast.File{f}, own)
}

// TestRcSlotReportsDirectWriteAfterSpawn proves the shared slot: the spawning
// task retains a handle to the same Box, and the write follows it in the block.
func TestRcSlotReportsDirectWriteAfterSpawn(t *testing.T) {
	src := `struct C {
    n: i32
}

struct Box {
    f: rc<C>
}

def use(b: rc<Box>) -> i32 {
    return 0
}

def main() -> i32 {
    var box: rc<Box> = new rc<Box>(Box { f: new rc<C>(C { n: 1 }) })
    var c: rc<C> = new rc<C>(C { n: 2 })
    region {
        var task: Task<i32> = spawn use(box)
        box.f = c
        _ = task
    }
    return 0
}
`
	ds := rcSlots(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeRcSlotUnguarded {
		t.Fatalf("diagnostics = %s, want one M0003", diag.FormatAll(ds))
	}
}

// TestRcSlotReportsMutMethodAfterSpawn proves the same slot through a method
// call: the method writes its receiver's rc field and the receiver is shared.
func TestRcSlotReportsMutMethodAfterSpawn(t *testing.T) {
	src := `struct C {
    n: i32
}

struct Box {
    f: rc<C>
}

def (mut b: Box) set(c: rc<C>) {
    b.f = c
}

def use(b: rc<Box>) -> i32 {
    return 0
}

def main() -> i32 {
    var box: rc<Box> = new rc<Box>(Box { f: new rc<C>(C { n: 1 }) })
    var c: rc<C> = new rc<C>(C { n: 2 })
    region {
        var task: Task<i32> = spawn use(box)
        box.set(c)
        _ = task
    }
    return 0
}
`
	ds := rcSlots(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeRcSlotUnguarded {
		t.Fatalf("diagnostics = %s, want one M0003", diag.FormatAll(ds))
	}
}

// TestRcSlotReportsNestedWrite checks that a nested block sees the shared
// state established before it.
func TestRcSlotReportsNestedWrite(t *testing.T) {
	src := `struct C {
    n: i32
}

struct Box {
    f: rc<C>
}

def use(b: rc<Box>) -> i32 {
    return 0
}

def main() -> i32 {
    var box: rc<Box> = new rc<Box>(Box { f: new rc<C>(C { n: 1 }) })
    var c: rc<C> = new rc<C>(C { n: 2 })
    region {
        var task: Task<i32> = spawn use(box)
        if true {
            box.f = c
        }
        _ = task
    }
    return 0
}
`
	ds := rcSlots(t, src)
	if len(ds) != 1 || ds[0].Code != diag.CodeRcSlotUnguarded {
		t.Fatalf("diagnostics = %s, want one M0003", diag.FormatAll(ds))
	}
}

// TestRcSlotAllowsNestedJoin checks that a join inside a child block clears the
// parent's shared state before an outer write.
func TestRcSlotAllowsNestedJoin(t *testing.T) {
	src := `struct C {
    n: i32
}

struct Box {
    f: rc<C>
}

def use(b: rc<Box>) -> i32 {
    return 0
}

def main() -> i32 {
    var box: rc<Box> = new rc<Box>(Box { f: new rc<C>(C { n: 1 }) })
    var c: rc<C> = new rc<C>(C { n: 2 })
    region {
        var task: Task<i32> = spawn use(box)
        if true {
            _ = task.join()!
        }
        box.f = c
    }
    return 0
}
`
	if ds := rcSlots(t, src); len(ds) != 0 {
		t.Fatalf("a write after a nested join was reported:\n%s", diag.FormatAll(ds))
	}
}

// TestRcSlotAllowsWriteBeforeSpawn checks that the slot was not shared yet.
func TestRcSlotAllowsWriteBeforeSpawn(t *testing.T) {
	src := `struct C {
    n: i32
}

struct Box {
    f: rc<C>
}

def use(b: rc<Box>) -> i32 {
    return 0
}

def main() -> i32 {
    var box: rc<Box> = new rc<Box>(Box { f: new rc<C>(C { n: 1 }) })
    var c: rc<C> = new rc<C>(C { n: 2 })
    box.f = c
    region {
        var task: Task<i32> = spawn use(box)
        _ = task
    }
    return 0
}
`
	if ds := rcSlots(t, src); len(ds) != 0 {
		t.Fatalf("a write before publication was reported:\n%s", diag.FormatAll(ds))
	}
}

// TestRcSlotAllowsNonRcField checks that only an rc slot is the subject.
func TestRcSlotAllowsNonRcField(t *testing.T) {
	src := `struct Box {
    n: i32
}

def use(b: rc<Box>) -> i32 {
    return 0
}

def main() -> i32 {
    var box: rc<Box> = new rc<Box>(Box { n: 1 })
    region {
        var task: Task<i32> = spawn use(box)
        box.n = 2
        _ = task
    }
    return 0
}
`
	if ds := rcSlots(t, src); len(ds) != 0 {
		t.Fatalf("a scalar field was reported:\n%s", diag.FormatAll(ds))
	}
}

// TestRcSlotAllowsJoinBeforeWrite checks that a completed task no longer keeps
// the handle shared.
func TestRcSlotAllowsJoinBeforeWrite(t *testing.T) {
	src := `struct C {
    n: i32
}

struct Box {
    f: rc<C>
}

def use(b: rc<Box>) -> i32 {
    return 0
}

def main() -> i32 {
    var box: rc<Box> = new rc<Box>(Box { f: new rc<C>(C { n: 1 }) })
    var c: rc<C> = new rc<C>(C { n: 2 })
    region {
        var task: Task<i32> = spawn use(box)
        _ = task.join()!
        box.f = c
    }
    return 0
}
`
	if ds := rcSlots(t, src); len(ds) != 0 {
		t.Fatalf("a write after join was reported:\n%s", diag.FormatAll(ds))
	}
}
