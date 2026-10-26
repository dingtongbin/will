package types

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
)

func TestFixedArrayLiteralChecksLengthAndElements(t *testing.T) {
	src := "func F() -> i32 {\n" +
		"    var xs: [3]i32 = [3]i32 { 1, 2, 3 }\n" +
		"    return xs[0]\n" +
		"}\n"
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("fixed array literal was rejected:\n%s", diag.FormatAll(ds))
	}
}

func TestFixedArrayLiteralRejectsWrongLength(t *testing.T) {
	src := "func F() -> i32 {\n    var xs: [3]i32 = [3]i32 { 1, 2 }\n    return xs[0]\n}\n"
	d := findCode(t, checkWithResolve(t, src), diag.CodeBadArgument)
	if d.Message != "wrong number of array elements" ||
		d.Help != "write exactly 3 elements" {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestExplicitTypeArgumentsInstantiateSignature(t *testing.T) {
	src := "func Id<T>(x: T) -> T {\n    return x\n}\n\nfunc Use() -> i32 {\n    return Id<i32>(3)\n}\n"
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("explicit type arguments did not instantiate the signature:\n%s", diag.FormatAll(ds))
	}
}

func TestLambdaBodyIsChecked(t *testing.T) {
	src := "func F() {\n    var f: func(i32) -> i32 = func(x: i32) -> i32 { return \"s\" }\n    _ = f\n}\n"
	d := findCode(t, checkWithResolve(t, src), diag.CodeTypeMismatch)
	if d.Message == "" {
		t.Fatal("the lambda body was not checked")
	}
}

func TestNegativeLiteralMayBeTheTypeMinimum(t *testing.T) {
	src := "func F() -> i64 {\n    var x: i64 = -9223372036854775808\n    return x\n}\n"
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("the smallest i64 was rejected:\n%s", diag.FormatAll(ds))
	}
}

func TestGenericStructFieldsAndMethods(t *testing.T) {
	src := "struct Box<T> {\n    value: T\n}\n\n" +
		"func (b: Box<T>) Get() -> T {\n    return b.value\n}\n\n" +
		"func main() -> i32 {\n    var b: Box<i32> = Box<i32> { value: 3 }\n    return b.Get()\n}\n"
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("a generic struct field and method were not instantiated:\n%s", diag.FormatAll(ds))
	}
}

func TestNoMethodOnUserType(t *testing.T) {
	src := "struct P {\n    x: i32\n}\n\nfunc F(p: P) -> i32 {\n    return p.Missing()\n}\n"
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeNoMethod)
	if d.Message != "no method Missing on P" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "check the method name and the receiver" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestBuiltinConstraintIsNotAValueType(t *testing.T) {
	src := "func F(e: Equal) {\n}\n"
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeBuiltinConstraintAsInterface)
	if d.Message != "Equal is a builtin constraint, not an interface" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "use it only as a type-parameter constraint" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestBuiltinConstraintInAConstraintPosition(t *testing.T) {
	src := "func Max<T: Ord>(a: T, b: T) -> T {\n    return a\n}\n"
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestNoField(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"read", "struct P {\n    x: i32\n}\nfunc F(p: P) {\n    _ = p.y\n}\n"},
		{"aggregate", "struct P {\n    x: i32\n}\nfunc F() {\n    var p: P = P { y: 1 }\n    _ = p\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := checkWithResolve(t, c.src)
			d := findCode(t, ds, diag.CodeNoField)
			if d.Message != "no field y on P" {
				t.Errorf("message = %q", d.Message)
			}
			if d.Help != "check the field name or the type" {
				t.Errorf("help = %q", d.Help)
			}
		})
	}
}

func TestSendResultMustBeHandled(t *testing.T) {
	src := `func F(ch: chan<i32>) {
    ch.Send(1)
}
`
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeResultDropped)
	if d.Message != "this Result is not handled" {
		t.Errorf("message = %q", d.Message)
	}
}

func TestNewWithoutInit(t *testing.T) {
	src := "struct P {\n    x: i32\n}\nfunc F() {\n    var p: P = new P(1)\n    _ = p\n}\n"
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeNewWithoutInit)
	if d.Message != "type P has no init; use a literal" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "write P { ... } or add an init method" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestNewWithInit(t *testing.T) {
	src := `struct P {
    x: i32
}
func (mut p: P) init(x: i32) -> Result<(), Error> {
    p.x = x
    return Ok(())
}
func F() {
    var p: P = new P(1)!
    _ = p
}
`
	ds := checkWithResolve(t, src)
	for _, d := range ds {
		if d.Code == diag.CodeNewWithoutInit {
			t.Fatalf("unexpected T0007:\n%s", diag.FormatAll(ds))
		}
	}
}

func TestBlockValueTakesExpectedType(t *testing.T) {
	src := `func F(c: bool) {
    var x: u8 = if c {
        1
    } else {
        2
    }
    _ = x
}
`
	ds := checkWithResolve(t, src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestBlockValueRange(t *testing.T) {
	src := `func F(c: bool) {
    var x: u8 = if c {
        1
    } else {
        300
    }
    _ = x
}
`
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeConstantRange)
	if d.Message != "constant 300 is out of range for u8" {
		t.Errorf("message = %q", d.Message)
	}
}

func TestBlockValueMismatch(t *testing.T) {
	src := `func F(c: bool) {
    _ = if c {
        1
    } else {
        "s"
    }
}
`
	ds := checkWithResolve(t, src)
	d := findCode(t, ds, diag.CodeTypeMismatch)
	if d.Message != "type mismatch: got str, want i32" {
		t.Errorf("message = %q", d.Message)
	}
}

func TestMatchValueTakesExpectedType(t *testing.T) {
	src := `func F(n: i32) {
    var x: i32 = match n {
        0 => 1
        _ => 2
    }
    _ = x
}
`
	ds := checkWithResolve(t, src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

// TestIndexGivesItsLiteralAWidth checks that an index expression is checked
// against usize. Without that, a literal inside an index keeps no type and the
// backend sees a pair of different widths for an arithmetic index.
func TestIndexGivesItsLiteralAWidth(t *testing.T) {
	src := `func Last(buf: []u8) -> u8 {
    return buf[buf.Len() - 1usize]
}
`
	f, res := parseResolve(t, src)
	tr, ds := CheckFull(f, res)
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
	if len(tr.LiteralTypes) != 1 {
		t.Fatalf("literal types = %d, want 1", len(tr.LiteralTypes))
	}
	for _, typ := range tr.LiteralTypes {
		if typ != "usize" {
			t.Errorf("literal type = %q, want usize", typ)
		}
	}
}

// TestIndexRejectsAWrongWidth checks that an index of the wrong type is
// reported at the index, not left to the backend.
func TestIndexRejectsAWrongWidth(t *testing.T) {
	src := `func Last(buf: []u8) -> u8 {
    var i: i32 = 0
    return buf[i]
}
`
	d := findCode(t, checkWithResolve(t, src), diag.CodeTypeMismatch)
	if d.Message != "type mismatch: got i32, want usize" {
		t.Errorf("message = %q", d.Message)
	}
}
