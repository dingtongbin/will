package types

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
)

func TestAsyncInterfaceRequiresAnAsyncImplementation(t *testing.T) {
	syncImpl := `interface Handler {
    async def handle() -> i32
}

struct S {
    n: i32

    def handle(self) -> i32 {
        return self.n
    }
}

def use(h: Handler) -> i32 {
    return 0
}

def f() {
    var s: S = S { n: 1 }
    var h: Handler = s
    _ = use(h)
}
`
	ds := checkTypes(t, syncImpl)
	if len(ds) != 1 || ds[0].Code != diag.CodeInterfaceUnsatisfied {
		t.Fatalf("sync implementation diagnostics:\n%s", diag.FormatAll(ds))
	}
	asyncImpl := `interface Handler {
    async def handle() -> i32
}

struct S {
    n: i32

    async def handle(self) -> i32 {
        return self.n
    }
}

def use(h: Handler) -> i32 {
    return 0
}

def f() {
    var s: S = S { n: 1 }
    var h: Handler = s
    _ = use(h)
}
`
	ds = checkTypes(t, asyncImpl)
	if len(ds) != 0 {
		t.Fatalf("async implementation diagnostics:\n%s", diag.FormatAll(ds))
	}
}

const ifaceProgram = `interface Shape {
    def area() -> i32
}

struct Square {
    s: i32

    def area(self) -> i32 {
        return self.s * self.s
    }
}


struct Dot {
    s: i32
}

def use(x: Shape) -> i32 {
    return x.area()
}

def f() {
    var ok: Shape = Square { s: 1 }
    _ = ok
    _ = use(Square { s: 1 })
    var bad: Shape = Dot { s: 1 }
    _ = bad
    _ = use(Dot { s: 1 })
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
			if d.Help != "missing method area" {
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
    def area() -> i32
}

def use(x: Shape) -> i32 {
    return x.area()
}
`
	ds := checkWithResolve(t, src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestEmptyInterface(t *testing.T) {
	src := `def check(value: interface {}) -> bool {
    return value.is<i32>()
}

def use() -> i32 {
    var value: interface {} = 7
    var number: Option<i32> = value.as<i32>()
    _ = check(value)
    return match number {
        Some(v) => v
        None => 0
    }
}
`
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestInterfaceDowncasts(t *testing.T) {
	src := `interface Shape {
    def area() -> i32
}

struct Square {
    s: i32

    def area(self) -> i32 {
        return self.s
    }
}


def use(x: Shape) -> i32 {
    var yes: bool = x.is<Square>()
    var maybe: Option<Square> = x.as<Square>()
    _ = yes
    return match maybe {
        Some(q) => q.area()
        None => 0
    }
}
`
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestInterfaceDowncastNeedsOneType(t *testing.T) {
	src := `interface Shape {
    def area() -> i32
}

def use(x: Shape) -> bool {
    return x.is()
}
`
	d := findCode(t, checkWithResolve(t, src), diag.CodeTypeArgCount)
	if d.Message != "wrong number of type arguments" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "a downcast takes one target type" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestInterfaceToInterface(t *testing.T) {
	narrow := `interface A {
    def f() -> i32
}

interface B {
    def f() -> i32
    def g() -> i32
}

def use(b: B) {
    var a: A = b
    _ = a
}
`
	for _, d := range checkWithResolve(t, narrow) {
		if d.Code == diag.CodeInterfaceUnsatisfied {
			t.Fatalf("B should satisfy A:\n%s", diag.FormatAll(checkWithResolve(t, narrow)))
		}
	}

	wider := `interface A {
    def f() -> i32
}

interface B {
    def f() -> i32
    def g() -> i32
}

def use(a: A) {
    var b: B = a
    _ = b
}
`
	d := findCode(t, checkWithResolve(t, wider), diag.CodeInterfaceUnsatisfied)
	if d.Help != "missing method g" {
		t.Errorf("help = %q, want missing method g", d.Help)
	}
}

func TestDispatchPoints(t *testing.T) {
	src := `interface Shape {
    def area() -> i32
}

def use(x: Shape) -> i32 {
    return x.area()
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
	src := `@derive(Show)
struct P {
    x: i32
}

def f() {
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

    def message(self) -> str {
        return "failed"
    }

    def status(self) -> i32 {
        return self.code
    }
}



def f() {
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

    def message(self) -> str {
        return "failed"
    }
}


def f() {
    var x: Error = E { code: 1 }
    _ = x
}
`
	d := findCode(t, checkWithResolve(t, bad), diag.CodeInterfaceUnsatisfied)
	if d.Help != "missing method status" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestInterfaceMethodSignatureMismatch(t *testing.T) {
	src := `interface Shape {
    def area() -> i32
}

struct Square {
    s: i32

    def area(self) -> i64 {
        return self.s
    }
}


def f() {
    var x: Shape = Square { s: 1 }
    _ = x
}
`
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeInterfaceUnsatisfied)
	if d.Help != "missing method area" {
		t.Errorf("help = %q", d.Help)
	}
}
