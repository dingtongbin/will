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

func TestBorrowTypeInterningAndText(t *testing.T) {
	in := newInterner()
	x := in.named("User", nil)
	ref := in.borrowed(x, ast.RefBorrow)
	ref2 := in.borrowed(in.named("User", nil), ast.RefBorrow)
	if ref != ref2 || ref.String() != "ref User" {
		t.Fatalf("ref User = %p %q", ref, ref.String())
	}
	mut := in.borrowed(x, ast.MutBorrow)
	if mut.String() != "mut User" || Equal(ref, mut) {
		t.Fatalf("mut User = %q, equal %v", mut.String(), Equal(ref, mut))
	}
	view := in.view(x, true)
	if view.String() != "mut []User" || !view.ViewMut() {
		t.Fatalf("mutable view = %q", view.String())
	}
}

func TestFromSourceRules(t *testing.T) {
	structSrc := "struct User { n: i32 }\n"
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			"two sources need from",
			"def better(a: ref User, b: ref User) -> ref User { return a }\n",
			diag.CodeBorrowOutlives,
		},
		{
			"explicit from is accepted",
			"def better(a: ref User, b: ref User) -> ref User from a, b { return a }\n",
			"",
		},
		{
			"single source is elided",
			"def first(a: ref User) -> ref User { return a }\n",
			"",
		},
		{
			"one view among owned parameters is elided",
			"struct Page { users: []User }\ndef page_of(users: []User, n: i32) -> Page { return Page { users: users } }\n",
			"",
		},
		{
			"from must cover every returned source",
			"def better(a: ref User, b: ref User) -> ref User from a { return b }\n",
			diag.CodeBorrowOutlives,
		},
		{
			"from self names the receiver",
			"struct Box { n: i32 }\ndef (b: ref Box) number() -> ref i32 from self { return ref b.n }\n",
			"",
		},
		{
			"from must name a parameter",
			"def first(a: ref User) -> ref User from tmp {\n    var tmp: User = User { n: 1 }\n    return a\n}\n",
			diag.CodeBorrowOutlives,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := checkTypes(t, structSrc+c.body)
			if c.code == "" {
				if len(ds) != 0 {
					t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
				}
				return
			}
			if len(ds) != 1 || ds[0].Code != c.code {
				t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
			}
		})
	}
}

func TestDefaultAndNamedArgumentTypes(t *testing.T) {
	base := `def add(a: i32, b: i32 = 2) -> i32 {
    return a + b
}
`
	ok := []string{
		"def f() -> i32 { return add(1) }\n",
		"def f() -> i32 { return add(b = 3, a = 4) }\n",
	}
	for _, body := range ok {
		if ds := checkWithResolve(t, base+body); len(ds) != 0 {
			t.Fatalf("valid call diagnostics:\n%s", diag.FormatAll(ds))
		}
	}
	bad := []string{
		"def f() -> i32 { return add() }\n",
		"def f() -> i32 { return add(c = 1) }\n",
		"def f() -> i32 { return add(a = 1, a = 2) }\n",
		"def f() -> i32 { return add(a = 1, 2) }\n",
	}
	for _, body := range bad {
		if ds := checkWithResolve(t, base+body); len(ds) == 0 {
			t.Errorf("invalid call accepted:\n%s", body)
		}
	}
	if ds := checkTypes(t, "def f(a: i32 = b, b: i32 = 2) -> i32 { return a + b }\n"); len(ds) == 0 {
		t.Fatal("a default referring to a later parameter was accepted")
	}
}

func TestSliceAndLenTypes(t *testing.T) {
	src := `
def read(xs: []i32) -> []i32 from xs {
    return xs[1:]
}

def write(xs: mut []i32) {
    var part: mut []i32 = mut xs[1:2]
    part[0] = 1
}

def count(xs: []i32) -> usize {
    return len(xs)
}
`
	ds := checkTypes(t, src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
	ds = checkTypes(t, "def f(xs: []i32) { var v: []i32 = xs[-1:2] }\n")
	if len(ds) == 0 {
		t.Fatal("negative slice bound was accepted")
	}
}

func TestSpawnMustBeAwaitedThroughANamedHandle(t *testing.T) {
	base := "def work() -> i32 {\n    return 1\n}\n\n"
	cases := []struct {
		name string
		body string
	}{
		{"statement", "def f() {\n    spawn work()\n}\n"},
		{"discard", "def f() {\n    _ = spawn work()\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := checkTypes(t, base+c.body)
			if len(ds) != 1 || ds[0].Code != diag.CodeTaskNotAwaited {
				t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
			}
		})
	}
	ds := checkTypes(t, base+"async def f() {\n    region {\n        var task: Task<i32> = spawn work()\n        _ = await task\n    }\n}\n")
	if len(ds) != 0 {
		t.Fatalf("named awaited task was rejected:\n%s", diag.FormatAll(ds))
	}
}

func TestConstantShiftAmount(t *testing.T) {
	src := `def f(n: i32, m: i64, b: u8) {
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
	src := `def f(n: i32) {
    _ = n << 31
    _ = n >> 0
    _ = n <<% 64
}
`
	if ds := checkTypes(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want 0:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestSlicingAFixedArrayYieldsAView(t *testing.T) {
	src := `def f(xs: [4]i32) -> i32 {
    var v: []i32 = xs[Range(0, 4)]
    return v[0]
}
`
	if ds := checkTypes(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want 0:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestChainedComparison(t *testing.T) {
	src := `def f(a: i32, b: i32, c: i32) {
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
	src := `def f() {
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
	ds := checkTypes(t, "def f() {\n    _ = 300\n}\n")
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
		{"var initializer", "def f() {\n    var x: i32 = \"s\"\n}\n", "got str, want i32"},
		{"return value", "def f() -> i32 {\n    return \"s\"\n}\n", "got str, want i32"},
		{"condition", "def f() {\n    var b: i32 = 1\n    if b {\n    }\n}\n", "got i32, want bool"},
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
	ds := checkWithResolve(t, "def g(a: i32) {\n}\ndef f() {\n    g(\"s\")\n}\n")
	d := findCode(t, ds, diag.CodeBadArgument)
	if d.Message != "argument 1 has type str, want i32" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "see the declaration of g" {
		t.Errorf("help = %q", d.Help)
	}
}

func TestWrongArgumentCount(t *testing.T) {
	ds := checkWithResolve(t, "def g(a: i32) {\n}\ndef f() {\n    g()\n}\n")
	d := findCode(t, ds, diag.CodeBadArgument)
	if d.Message != "wrong number of arguments" {
		t.Errorf("message = %q", d.Message)
	}
}

func TestWrongTypeArgumentCount(t *testing.T) {
	ds := checkWithResolve(t, "def g<T>(a: T) {\n}\ndef f() {\n    g<i32, i64>(1)\n}\n")
	d := findCode(t, ds, diag.CodeTypeArgCount)
	if d.Message != "wrong number of type arguments" || !strings.Contains(d.Help, "1") {
		t.Errorf("diagnostic = %+v", d)
	}
}

func TestUntypedConstantRangeUsesExpectedType(t *testing.T) {
	ds := checkWithResolve(t, "def f() {\n    var x: u8 = 300\n}\n")
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

func TestAwaitRules(t *testing.T) {
	ds := checkWithResolve(t, `async def load() -> i32 { return 1 }
def use() -> i32 { return await load() }
`)
	findCode(t, ds, "K0009")

	ds = checkWithResolve(t, `async def load() -> i32 { return 1 }
def use() -> i32 { return load() }
`)
	findCode(t, ds, "K0010")

	ds = checkWithResolve(t, `async def use() -> i32 { return await 1 }
`)
	findCode(t, ds, "K0011")

	ds = checkWithResolve(t, `async def load() -> i32 { return 1 }
async def use() -> i32 { return await load() }
`)
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want 0:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestGenericClassConstructorSubstitutesFields(t *testing.T) {
	src := `class Box<T> {
    value: T
    tag: i32

    def get(self) -> T {
        return self.value.clone()
    }
}

def main() -> i32 {
    var b: Box<i32> = Box<i32>(value=7, tag=1)
    return b.get() + b.tag
}
`
	if ds := checkWithResolve(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want 0:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestGenericClassConstructorRejectsWrongFieldType(t *testing.T) {
	src := `class Box<T> {
    value: T
}

def main() -> i32 {
    var b: Box<i32> = Box<i32>(value="wrong")
    return b.value
}
`
	ds := checkWithResolve(t, src)
	findCode(t, ds, diag.CodeTypeMismatch)
}
