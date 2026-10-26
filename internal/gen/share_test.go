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

interface Has {
    func Get() -> i32
}

struct Holder {
    c: rc<C>
}

func (h: Holder) Get() -> i32 {
    return h.c.n
}

func TakeMap(m: Map<str, rc<C>>) -> i32 {
    return 0
}

func TakeSet(s: Set<rc<C>>) -> i32 {
    return 0
}

func TakeArray(a: [2]rc<C>) -> i32 {
    return 0
}

func TakeEnum(e: E) -> i32 {
    return 0
}

func TakeFunc(f: func() -> i32) -> i32 {
    return f()
}

func TakeIface(v: Has) -> i32 {
    return v.Get()
}

func main() -> i32 {
    var c: rc<C> = new rc<C>(C { n: 1 })
    var m: Map<str, rc<C>> = new Map<str, rc<C>>()
    m.Set("a".Clone(), c.Clone())
    var s: Set<rc<C>> = new Set<rc<C>>()
    s.Add(c.Clone())
    var a: [2]rc<C> = [2]rc<C> { c.Clone(), c.Clone() }
    var e: E = E.One(c.Clone())
    var f: func() -> i32 = func() -> i32 {
        return c.n
    }
    var h: Holder = Holder { c: c.Clone() }
    var v: Has = h
    var tm: Task<i32> = spawn TakeMap(m)
    var ts: Task<i32> = spawn TakeSet(s)
    var ta: Task<i32> = spawn TakeArray(a)
    var te: Task<i32> = spawn TakeEnum(e)
    var tf: Task<i32> = spawn TakeFunc(f)
    var tv: Task<i32> = spawn TakeIface(v)
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
