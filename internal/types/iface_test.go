package types

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
)

const ifaceProgram = `interface Shape {
    func Area() -> i32
}

struct Square {
    s: i32
}

func (q: Square) Area() -> i32 {
    return q.s * q.s
}

struct Dot {
    s: i32
}

func Use(x: Shape) -> i32 {
    return x.Area()
}

func F() {
    var ok: Shape = Square { s: 1 }
    _ = ok
    _ = Use(Square { s: 1 })
    var bad: Shape = Dot { s: 1 }
    _ = bad
    _ = Use(Dot { s: 1 })
}
`

func TestInterfaceSatisfaction(t *testing.T) {
	ds := checkWithResolve(t, ifaceProgram)
	unsatisfied := 0
	for _, d := range ds {
		if d.Code == diag.CodeInterfaceUnsatisfied {
			unsatisfied++
			if d.Message != "type Dot does not satisfy Shape" {
				t.Errorf("message = %q", d.Message)
			}
			if d.Help != "missing method Area" {
				t.Errorf("help = %q", d.Help)
			}
		}
	}
	if unsatisfied != 2 {
		t.Fatalf("T0002 count = %d, want 2 (var and argument):\n%s", unsatisfied, diag.FormatAll(ds))
	}
}

func TestInterfaceMethodCall(t *testing.T) {
	src := `interface Shape {
    func Area() -> i32
}

func Use(x: Shape) -> i32 {
    return x.Area()
}
`
	ds := checkWithResolve(t, src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestDispatchPoints(t *testing.T) {
	src := `interface Shape {
    func Area() -> i32
}

func Use(x: Shape) -> i32 {
    return x.Area()
}
`
	f, res := parseResolve(t, src)
	tr, _ := CheckFull(f, res)
	if len(tr.Dispatch) != 1 {
		t.Fatalf("dispatch points = %d, want 1", len(tr.Dispatch))
	}
	if tr.Dispatch[0].Interface != "Shape" {
		t.Errorf("interface = %q, want Shape", tr.Dispatch[0].Interface)
	}
}

func TestDerivedShowSatisfiesShow(t *testing.T) {
	src := `struct P {
    x: i32
}

derive Show for P

func F() {
    var s: Show = P { x: 1 }
    _ = s
}
`
	ds := checkWithResolve(t, src)
	for _, d := range ds {
		if d.Code == diag.CodeInterfaceUnsatisfied {
			t.Fatalf("derive Show did not satisfy Show:\n%s", diag.FormatAll(ds))
		}
	}
}

func TestBuiltinErrorInterface(t *testing.T) {
	ok := `struct E {
    code: i32
}

func (e: E) Message() -> str {
    return "failed"
}

func (e: E) Status() -> i32 {
    return e.code
}

func F() {
    var x: Error = E { code: 1 }
    _ = x
}
`
	for _, d := range checkWithResolve(t, ok) {
		if d.Code == diag.CodeInterfaceUnsatisfied {
			t.Fatalf("Error was not satisfied:\n%s", diag.FormatAll(checkWithResolve(t, ok)))
		}
	}

	bad := `struct E {
    code: i32
}

func (e: E) Message() -> str {
    return "failed"
}

func F() {
    var x: Error = E { code: 1 }
    _ = x
}
`
	d := findCode(t, checkWithResolve(t, bad), diag.CodeInterfaceUnsatisfied)
	if d.Help != "missing method Status" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestInterfaceMethodSignatureMismatch(t *testing.T) {
	src := `interface Shape {
    func Area() -> i32
}

struct Square {
    s: i32
}

func (q: Square) Area() -> i64 {
    return q.s
}

func F() {
    var x: Shape = Square { s: 1 }
    _ = x
}
`
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeInterfaceUnsatisfied)
	if d.Help != "missing method Area" {
		t.Errorf("help = %q", d.Help)
	}
}
