package types

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
)

func TestInterningMakesEqualityIdentity(t *testing.T) {
	in := newInterner()
	a := in.scalar("i32")
	b := in.scalar("i32")
	if a != b || a.ID() != b.ID() {
		t.Fatalf("scalar i32 is not interned: %p %p", a, b)
	}
	arr1 := in.named("Array", []*Type{a})
	arr2 := in.named("Array", []*Type{in.scalar("i32")})
	if arr1 != arr2 {
		t.Fatalf("Array<i32> is not interned")
	}
	if arr1.String() != "Array<i32>" {
		t.Errorf("text = %q, want Array<i32>", arr1.String())
	}
	if Equal(arr1, in.named("Array", []*Type{in.scalar("i64")})) {
		t.Errorf("Array<i32> equals Array<i64>")
	}
	if !Equal(arr1, nil) {
		t.Errorf("an unknown type must match anything")
	}
	m := in.named("Map", []*Type{in.scalar("str"), in.named("Value", nil)})
	if m.String() != "Map<str,Value>" {
		t.Errorf("text = %q, want Map<str,Value>", m.String())
	}
	fn := in.funcT([]*Type{in.scalar("i64")}, in.scalar("bool"))
	if fn.String() != "func(i64)->bool" {
		t.Errorf("text = %q, want func(i64)->bool", fn.String())
	}
	if in.view(in.scalar("u8")).String() != "[]u8" {
		t.Errorf("view text = %q, want []u8", in.view(in.scalar("u8")).String())
	}
	if in.unit().String() != "()" {
		t.Errorf("unit text = %q, want ()", in.unit().String())
	}
}

func TestIntConstRanges(t *testing.T) {
	cases := []struct {
		text   string
		want   uint64
		suffix string
	}{
		{"0", 0, ""},
		{"255", 255, ""},
		{"0xFF", 255, ""},
		{"0b1010", 10, ""},
		{"0o17", 15, ""},
		{"1_000", 1000, ""},
		{"18446744073709551615", 18446744073709551615, ""},
	}
	for _, c := range cases {
		v, suffix, ok := parseIntLiteral(c.text)
		if !ok || v.overflow || v.mag != c.want || suffix != c.suffix {
			t.Errorf("parse %q = (%d, %v, %q), want (%d, false, %q)", c.text, v.mag, v.overflow, suffix, c.want, c.suffix)
		}
	}
	v, _, ok := parseIntLiteral("18446744073709551616")
	if !ok || !v.overflow {
		t.Errorf("parse one past u64 max = %+v, want overflow", v)
	}
	if _, _, ok := parseIntLiteral("0xZZ"); ok {
		t.Errorf("parse 0xZZ succeeded, want failure")
	}
}

func TestFitsInt(t *testing.T) {
	cases := []struct {
		value uint64
		typ   string
		want  bool
	}{
		{127, "i8", true},
		{128, "i8", false},
		{255, "u8", true},
		{256, "u8", false},
		{2147483647, "i32", true},
		{2147483648, "i32", false},
		{9223372036854775807, "i64", true},
		{9223372036854775808, "i64", false},
		{9223372036854775808, "u64", true},
	}
	for _, c := range cases {
		if got := fitsInt(intConst{mag: c.value}, c.typ); got != c.want {
			t.Errorf("fits %d in %s = %v, want %v", c.value, c.typ, got, c.want)
		}
	}
	if fitsInt(intConst{overflow: true}, "u64") {
		t.Errorf("an overflowing constant fits u64")
	}
}

func checkTypes(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse diagnostics:\n%s", diag.FormatAll(ds))
	}
	return Check(f, nil)
}

func TestConstantShiftAmount(t *testing.T) {
	src := `func F(n: i32, m: i64, b: u8) {
    _ = n << 32
    _ = m >> 64
    _ = b << 9
}
`
	ds := checkTypes(t, src)
	if len(ds) != 3 {
		t.Fatalf("diagnostics = %d, want 3:\n%s", len(ds), diag.FormatAll(ds))
	}
	want := []struct {
		msg  string
		help string
	}{
		{"shift amount 32 is not less than the width of i32",
			"shift by less than 32, or write <<% to truncate"},
		{"shift amount 64 is not less than the width of i64",
			"shift by less than 64, or write <<% to truncate"},
		{"shift amount 9 is not less than the width of u8",
			"shift by less than 8, or write <<% to truncate"},
	}
	for i, d := range ds {
		if d.Code != diag.CodeShiftAmount || d.Message != want[i].msg || d.Help != want[i].help {
			t.Errorf("diagnostic %d = %+v", i, d)
		}
	}
}

func TestShiftAmountBelowWidthAndTruncatingShift(t *testing.T) {
	src := `func F(n: i32) {
    _ = n << 31
    _ = n >> 0
    _ = n <<% 64
}
`
	if ds := checkTypes(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want 0:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestChainedComparison(t *testing.T) {
	src := `func F(a: i32, b: i32, c: i32) {
    _ = a < b < c
}
`
	ds := checkTypes(t, src)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want 1:\n%s", len(ds), diag.FormatAll(ds))
	}
	d := ds[0]
	if d.Code != diag.CodeChainedComparison || d.Message != "comparisons cannot be chained" ||
		d.Help != "split it into two comparisons" {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestIntegerConstantRange(t *testing.T) {
	src := `func F() {
    var a: u8 = 300u8
    var b: i8 = 120i8
}
`
	ds := checkTypes(t, src)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %d, want 1:\n%s", len(ds), diag.FormatAll(ds))
	}
	d := ds[0]
	if d.Code != diag.CodeConstantRange {
		t.Fatalf("code = %s, want T0005", d.Code)
	}
	if d.Message != "constant 300u8 is out of range for u8" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "write a suffix, or a smaller value" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestUntypedConstantFitsDefault(t *testing.T) {
	ds := checkTypes(t, "func F() {\n    _ = 300\n}\n")
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func parseResolve(t *testing.T, src string) (*ast.File, *resolve.Result) {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse diagnostics:\n%s", diag.FormatAll(ds))
	}
	res, _ := resolve.Resolve(f)
	return f, res
}

func checkWithResolve(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse diagnostics:\n%s", diag.FormatAll(ds))
	}
	// Resolve only provides the reference table here; its own diagnostics are
	// checked by the resolve tests.
	res, _ := resolve.Resolve(f)
	return Check(f, res)
}

func TestTypeMismatch(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"var initializer", "func F() {\n    var x: i32 = \"s\"\n}\n", "got str, want i32"},
		{"return value", "func F() -> i32 {\n    return \"s\"\n}\n", "got str, want i32"},
		{"condition", "func F() {\n    var b: i32 = 1\n    if b {\n    }\n}\n", "got i32, want bool"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := checkWithResolve(t, c.src)
			found := false
			for _, d := range ds {
				if d.Code == diag.CodeTypeMismatch && strings.Contains(d.Message, c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing T0001 %q in:\n%s", c.want, diag.FormatAll(ds))
			}
		})
	}
}

func TestBadArgument(t *testing.T) {
	ds := checkWithResolve(t, "func G(a: i32) {\n}\nfunc F() {\n    G(\"s\")\n}\n")
	d := findCode(t, ds, diag.CodeBadArgument)
	if d.Message != "argument 1 has type str, want i32" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "see the declaration of G" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestWrongArgumentCount(t *testing.T) {
	ds := checkWithResolve(t, "func G(a: i32) {\n}\nfunc F() {\n    G()\n}\n")
	d := findCode(t, ds, diag.CodeBadArgument)
	if d.Message != "wrong number of arguments" {
		t.Errorf("message = %q", d.Message)
	}
}

func TestWrongTypeArgumentCount(t *testing.T) {
	ds := checkWithResolve(t, "func G<T>(a: T) {\n}\nfunc F() {\n    G<i32, i64>(1)\n}\n")
	d := findCode(t, ds, diag.CodeTypeArgCount)
	if d.Message != "wrong number of type arguments" || !strings.Contains(d.Help, "1") {
		t.Errorf("diagnostic = %+v", d)
	}
}

func TestUntypedConstantRangeUsesExpectedType(t *testing.T) {
	ds := checkWithResolve(t, "func F() {\n    var x: u8 = 300\n}\n")
	d := findCode(t, ds, diag.CodeConstantRange)
	if d.Message != "constant 300 is out of range for u8" {
		t.Errorf("message = %q", d.Message)
	}
}

func findCode(t *testing.T, ds []diag.Diagnostic, code string) diag.Diagnostic {
	t.Helper()
	for _, d := range ds {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("missing %s in:\n%s", code, diag.FormatAll(ds))
	return diag.Diagnostic{}
}
