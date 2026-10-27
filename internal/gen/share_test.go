package gen

import (
	"strings"
	"testing"
)

// TestCrossTaskShareCoversContainers checks that every owning shape that can
// carry an rc handle promotes it before the value enters a spawned task. The
// direct rc parameter case is covered by tests/safety/rcshare; this test covers
// the shapes that previously fell through shareValue.
func TestCrossTaskShareCoversContainers(t *testing.T) {
	src := `struct C {
    n: i32
}

enum E {
    Empty
    One(rc<C>)
}

interface has {
    def get() -> i32
}

struct Holder {
    c: rc<C>
}

def (h: Holder) get() -> i32 {
    return h.c.n
}

def take_map(m: Map<str, rc<C>>) -> i32 {
    return 0
}

def take_set(s: Set<rc<C>>) -> i32 {
    return 0
}

def take_array(a: [2]rc<C>) -> i32 {
    return 0
}

def take_enum(e: E) -> i32 {
    return 0
}

def take_func(f: func() -> i32) -> i32 {
    return f()
}

def take_iface(v: has) -> i32 {
    return v.get()
}

def main() -> i32 {
    var c: rc<C> = new rc<C>(C { n: 1 })
    var m: Map<str, rc<C>> = new Map<str, rc<C>>()
    m.set("a".clone(), c.clone())
    var s: Set<rc<C>> = new Set<rc<C>>()
    s.add(c.clone())
    var a: [2]rc<C> = [2]rc<C> { c.clone(), c.clone() }
    var e: E = E.One(c.clone())
    var f: func() -> i32 = func() -> i32 {
        return c.n
    }
    var h: Holder = Holder { c: c.clone() }
    var v: has = h
    var tm: Task<i32> = spawn take_map(m)
    var ts: Task<i32> = spawn take_set(s)
    var ta: Task<i32> = spawn take_array(a)
    var te: Task<i32> = spawn take_enum(e)
    var tf: Task<i32> = spawn take_func(f)
    var tv: Task<i32> = spawn take_iface(v)
    _ = tm
    _ = ts
    _ = ta
    _ = te
    _ = tf
    _ = tv
    return 0
}
`
	ir := emitShareIR(t, src)
	for _, want := range []string{
		`share.entries.cond`,
		`share.enum.`,
		`call void @willrt_closure_share(ptr`,
		`define void @"example.com/app.iface.share.`,
		`call void %`,
	} {
		if !strings.Contains(ir, want) {
			t.Errorf("IR lacks %q:\n%s", want, ir)
		}
	}
	if strings.Count(ir, "call void @willrt_rc_share") < 6 {
		t.Errorf("expected a promotion for each owning shape, got %d calls:\n%s",
			strings.Count(ir, "call void @willrt_rc_share"), ir)
	}
}

// emitShareIR lowers one module and returns the normalized IR.
func emitShareIR(t *testing.T, src string) string {
	t.Helper()
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	return normalizeIR(string(ir))
}
