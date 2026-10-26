package gen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/types"
	"github.com/will-lang/will/internal/willc"
)

// normalizeIR hides linkage and inline attributes from tests that assert the
// function signatures. Those properties are covered by the IR verifier and by
// the linkage tests, not by every shape test.
func normalizeIR(text string) string {
	text = strings.ReplaceAll(text, "define hidden ", "define ")
	text = strings.ReplaceAll(text, "define internal ", "define ")
	text = strings.ReplaceAll(text, " alwaysinline", "")
	text = strings.ReplaceAll(text, " inlinehint", "")
	return text
}

func emitWillc(t *testing.T, src string) *willc.Doc {
	t.Helper()
	return emitWillcAs(t, src, "example.com/app")
}

// emitWillcAs emits one source file under a chosen module path, so a test can
// build a module of more than one document.
func emitWillcAs(t *testing.T, src, module string) *willc.Doc {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %d diagnostics", len(ds))
	}
	res, _ := resolve.Resolve(f)
	checked, _ := types.CheckFull(f, res)
	data, err := willc.Emit(f, res, module, nil, checked.LiteralTypes, checked.CallTypes, checked.NameTypes, nil)
	if err != nil {
		t.Fatalf("emit willc: %v", err)
	}
	doc, err := willc.Read(data)
	if err != nil {
		t.Fatalf("read willc: %v", err)
	}
	return doc
}

func TestEmitIntegerFunction(t *testing.T) {
	doc := emitWillc(t, "func Add(a: i32, b: i32) -> i32 {\n    return a + b\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.Add"(i32 %a, i32 %b) {`,
		"entry:",
		"call {i32, i1} @llvm.sadd.with.overflow.i32(i32 %a, i32 %b)",
		"extractvalue {i32, i1}",
		"call void @willrt_panic(ptr @.str.0, ptr @.str.1, ptr @.str.2)",
		`@.str.0 = private unnamed_addr constant [17 x i8] c"integer overflow\00"`,
		"ret i32 %t2",
		"}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitIndexChecksTheBound(t *testing.T) {
	src := "func At(xs: []i32, i: usize) -> i32 {\n    return xs[i]\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"icmp uge i64 %i, %t",
		`c"index out of bounds\00"`,
		"getelementptr inbounds i32, ptr %t",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitSignedIndexChecksBelowZero(t *testing.T) {
	src := "func At(xs: [2]i32, i: i32) -> i32 {\n    return xs[i]\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"icmp slt i32 %i, 0",
		"sext i32 %i to i64",
		"icmp uge i64 %t2, 2",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitSliceChecksBothBounds(t *testing.T) {
	src := "func Mid(xs: []i32, lo: usize, hi: usize) -> i32 {\n    return xs[Range(lo, hi)][0]\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"icmp ugt i64 %lo, %hi",
		"icmp ugt i64 %hi, %t",
		`c"slice bounds out of range\00"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitEnumConstructionAndMatch(t *testing.T) {
	src := "enum E {\n    A\n    B(i32)\n}\n\n" +
		"func F(e: E) -> i32 {\n    return match e {\n        A => 0\n        B(v) => v\n    }\n}\n\n" +
		"func Make() -> E {\n    return E.B(7)\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`%"enum.example.com/app.E" = type { i32, i32 }`,
		"define i32 @\"example.com/app.F\"(ptr %e) {",
		"alloca i32",
		"getelementptr inbounds %\"enum.example.com/app.E\", ptr %e, i32 0, i32 0",
		"icmp eq i32 %t2, 0",
		"icmp eq i32 %t5, 1",
		"getelementptr inbounds %\"enum.example.com/app.E\", ptr %e, i32 0, i32 1",
		"define %\"enum.example.com/app.E\" @\"example.com/app.Make\"() {",
		"store i32 1, ptr",
		"store i32 7, ptr",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitUnqualifiedEnumVariants(t *testing.T) {
	src := "enum E {\n    A\n    B(i32)\n}\n\n" +
		"func Payload() -> E {\n    return B(7)\n}\n\n" +
		"func Empty() -> E {\n    return A\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define %"enum.example.com/app.E" @"example.com/app.Payload"() {`,
		`define %"enum.example.com/app.E" @"example.com/app.Empty"() {`,
		"store i32 1, ptr",
		"store i32 0, ptr",
		"store i32 7, ptr",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitNewInterfaceArgument(t *testing.T) {
	src := "interface Value {\n    func Get() -> i32\n}\n\n" +
		"struct Item {\n    n: i32\n}\n\n" +
		"func (i: Item) Get() -> i32 {\n    return i.n\n}\n\n" +
		"struct Holder {\n    value: Value\n}\n\n" +
		"func (mut h: Holder) init(value: Value) {\n    h.value = value\n}\n\n" +
		"func Use() -> i32 {\n" +
		"    var item: Item = Item { n: 7 }\n" +
		"    var holder: Holder = new Holder(item)\n" +
		"    return holder.value.Get()\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call void @"example.com/app.Holder.init"`,
		`store ptr @"example.com/app.vtable.Value.Item"`,
		`getelementptr inbounds %"interface.example.com/app.Value"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitMatchWithGuardAndWildcard(t *testing.T) {
	src := "func Pick(n: i32) -> i32 {\n    return match n {\n        0 => 10\n        x if x > 0 => x\n        _ => -1\n    }\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"icmp eq i32 %n, 0",
		"icmp sgt i32 %n, 0",
		"match.guard.",
		"store i32 10, ptr %v.match.",
		"store i32 %n, ptr %v.match.",
		"br i1 true, label %match.arm.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitStrLiteralIndexAndLoop(t *testing.T) {
	src := "func At(s: str, i: i32) -> u32 {\n    return s[i]\n}\n\n" +
		"func One() -> u32 {\n    return At(\"hi\", 0)\n}\n\n" +
		"func Count(s: str) -> i32 {\n    var n: i32 = 0\n    for i, c in s {\n        if c == 65u32 { n += 1 }\n        _ = i\n    }\n    return n\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`@.bytes.1 = private unnamed_addr constant`,
		"call i32 @willrt_utf8_decode(ptr",
		`c"invalid UTF-8\00"`,
		"for.cond.1:",
		"icmp ult i64",
		"sext i32",
		"declare i32 @willrt_utf8_decode(ptr, i64, ptr)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitGenericStructInstance(t *testing.T) {
	src := "struct Box<T> {\n    value: T\n}\n\n" +
		"func Read(b: Box<i32>) -> i32 {\n    return b.value\n}\n\n" +
		"func main() -> i32 {\n    var b: Box<i32> = Box<i32> { value: 3 }\n    return Read(b)\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`%"struct.example.com/app.Box$i32" = type { i32 }`,
		`getelementptr inbounds %"struct.example.com/app.Box$i32"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitGenericMethodInstance(t *testing.T) {
	src := "struct Box<T> {\n    value: T\n}\n\n" +
		"func (b: Box<T>) Get() -> T {\n    return b.value\n}\n\n" +
		"func main() -> i32 {\n    var b: Box<i32> = Box<i32> { value: 3 }\n    return b.Get()\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.Box<i32>.Get"(ptr %b)`,
		`call i32 @"example.com/app.Box<i32>.Get"(`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, `%"struct.example.com/app.Box$i32" = type`) != 1 {
		t.Errorf("the generic struct type was emitted more than once:\n%s", text)
	}
}

func TestEmitFunctionValue(t *testing.T) {
	src := "func Apply(f: func(i32) -> i32, x: i32) -> i32 {\n" +
		"    return f(x)\n" +
		"}\n\n" +
		"func main() -> i32 {\n" +
		"    var inc: func(i32) -> i32 = func(x: i32) -> i32 { return x + 1 }\n" +
		"    return Apply(inc, 1)\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.lambda.1"(ptr %closure.env, i32 %x)`,
		`define i32 @"example.com/app.Apply"(ptr %f, i32 %x)`,
		`call i32 %t3(ptr %t4, i32 %x)`,
		`store ptr @"example.com/app.lambda.1"`,
		`store ptr null`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitCapturingClosure(t *testing.T) {
	src := "func Counter() -> func() -> i32 {\n" +
		"    var n: i32 = 0\n" +
		"    return func() -> i32 {\n" +
		"        n += 1\n" +
		"        return n\n" +
		"    }\n" +
		"}\n\n" +
		"func main() -> i32 {\n" +
		"    var c: func() -> i32 = Counter()\n" +
		"    var a: i32 = c()\n" +
		"    return a\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`getelementptr { ptr, i64, i32 }, ptr null, i32 1`,
		`call ptr @willrt_closure_new(i64`,
		`define i32 @"example.com/app.lambda.1"(ptr %closure.env)`,
		`getelementptr inbounds { ptr, i64, i32 }, ptr %closure.env, i32 0, i32 2`,
		`define void @"example.com/app.lambda.drop.1"(ptr %env)`,
		`call void @willrt_closure_release(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\\n%s", want, text)
		}
	}
}

func TestEmitNamedFunctionValue(t *testing.T) {
	src := "func Inc(x: i32) -> i32 {\n" +
		"    return x + 1\n" +
		"}\n\n" +
		"func Apply(f: func(i32) -> i32, x: i32) -> i32 {\n" +
		"    return f(x)\n" +
		"}\n\n" +
		"func main() -> i32 {\n" +
		"    return Apply(Inc, 4)\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.thunk.Inc"(ptr %closure.env, i32 %x)`,
		`call i32 @"example.com/app.Inc"(i32 %x)`,
		`store ptr @"example.com/app.thunk.Inc"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\\n%s", want, text)
		}
	}
}

func TestEmitRcHandles(t *testing.T) {
	src := "struct Counter {\n" +
		"    n: i32\n" +
		"}\n\n" +
		"func (mut c: Counter) Add(d: i32) {\n" +
		"    c.n += d\n" +
		"}\n\n" +
		"func Identity(c: rc<Counter>) -> rc<Counter> {\n" +
		"    return c\n" +
		"}\n\n" +
		"func main() -> i32 {\n" +
		"    var c: rc<Counter> = new rc<Counter>(Counter { n: 1 })\n" +
		"    c.Add(1)\n" +
		"    var d: rc<Counter> = c\n" +
		"    var w: weak<Counter> = c.Weak()\n" +
		"    var u: Option<rc<Counter>> = w.Upgrade()\n" +
		"    var e: rc<Counter> = Identity(d)\n" +
		"    _ = e\n" +
		"    _ = u\n" +
		"    return c.n\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call i64 @willrt_rc_new(ptr`,
		`call void @willrt_rc_retain(i64`,
		`call void @willrt_rc_release(i64`,
		`call i64 @willrt_weak_new(i64`,
		`call i32 @willrt_weak_upgrade(i64`,
		`call ptr @willrt_rc_deref(i64`,
		`define void @"example.com/app.Counter.Add"(ptr %c, i32 %d)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitVariadicCall(t *testing.T) {
	src := "func Sum(xs: ...i32) -> i32 {\n" +
		"    var total: i32 = 0\n" +
		"    for x in xs {\n" +
		"        total += x\n" +
		"    }\n" +
		"    return total\n" +
		"}\n\n" +
		"func main() -> i32 {\n" +
		"    return Sum(1, 2, 3)\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.Sum"(ptr %xs)`,
		`call ptr @willrt_plate_new(i64 3, i64`,
		`store i32 1, ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitSelect(t *testing.T) {
	src := "func main() -> i32 {\n" +
		"    var a: chan<i32> = new chan<i32>(1)\n" +
		"    var v: i32 = 0\n" +
		"    select {\n" +
		"        x = a.Recv() => {\n" +
		"            v = 1\n" +
		"        }\n" +
		"        default => {\n" +
		"            v = 2\n" +
		"        }\n" +
		"    }\n" +
		"    return v\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call i32 @willrt_chan_try_recv(ptr`,
		`select.fast.dispatch.`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{
		`call ptr @willrt_select_start(ptr`,
		`call i32 @willrt_select_poll(ptr`,
		`call void @willrt_select_set_status(i32`,
	} {
		if strings.Contains(text, unwanted) {
			t.Errorf("a ready select still enters the waiter path %q:\n%s", unwanted, text)
		}
	}
}

func TestFloatLiteralKeepsADecimalPoint(t *testing.T) {
	cases := map[string]string{
		"1e-6": "1.0e-6",
		"1E9":  "1.0E9",
		"1.5":  "1.5",
		"2f64": "2.0",
	}
	for text, want := range cases {
		if got := literalFloatText(text); got != want {
			t.Errorf("literalFloatText(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestDurationLiteral(t *testing.T) {
	cases := map[string]string{
		"5ms": "5000000",
		"2s":  "2000000000",
		"1us": "1000",
		"3m":  "180000000000",
	}
	for text, want := range cases {
		got, ok := durationNanos(text)
		if !ok || got != want {
			t.Errorf("durationNanos(%q) = %q, %v; want %q", text, got, ok, want)
		}
	}
	if _, ok := durationNanos("5x"); ok {
		t.Error("durationNanos accepted an unknown unit")
	}
}

func TestEmitPrintAndInterpolation(t *testing.T) {
	src := "func main() -> i32 {\n" +
		"    var n: i32 = 3\n" +
		"    var ok: bool = true\n" +
		"    var x: f64 = 1.5\n" +
		"    var s: str = f\"n={n} ok={ok} x={x}\"\n" +
		"    print(s, \"\\n\")\n" +
		"    return 0\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call i64 @willrt_fmt_i64_len(i64`,
		`call i64 @willrt_fmt_bool_len(i32`,
		`call i64 @willrt_fmt_f64_len(double`,
		`call i64 @willrt_fmt_i64_into(ptr`,
		`call void @willrt_write_stdout(ptr`,
		`call ptr @willrt_alloc(i64`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitStrConcatAndSlice(t *testing.T) {
	src := "func main() -> i32 {\n" +
		"    var a: str = \"foo\"\n" +
		"    var b: str = \"bar\"\n" +
		"    var c: str = a + b\n" +
		"    var d: []u8 = c[Range(0, 3)]\n" +
		"    return i32(c.Len()) + i32(d.Len())\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call ptr @willrt_alloc(i64 %t`,
		`call void @memcpy(ptr`,
		`declare void @memcpy(ptr, ptr, i64)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitMapContainer(t *testing.T) {
	src := "func main() -> i32 {\n" +
		"    var m: Map<i32, i32> = new Map<i32, i32>()\n" +
		"    m.Set(1, 10)\n" +
		"    var s: i32 = 0\n" +
		"    for v in m {\n        s = s + v\n    }\n" +
		"    if m.Has(1) {\n        s = s + 1\n    }\n" +
		"    return s\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`alloca { ptr, i64, i64 }`,
		`getelementptr inbounds { i32, i32 }`,
		`call ptr @willrt_alloc(i64`,
		`phi i64 [ %t`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitSetContainer(t *testing.T) {
	src := "func main() -> i32 {\n" +
		"    var s: Set<i32> = new Set<i32>()\n" +
		"    s.Add(1)\n" +
		"    var n: i32 = 0\n" +
		"    for v in s {\n        n = n + v\n    }\n" +
		"    if s.Delete(1) {\n        n = n + 1\n    }\n" +
		"    return n\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`alloca { ptr, i64, i64 }`,
		`call void @memmove(ptr`,
		`declare void @memmove(ptr, ptr, i64)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitGenericFunctionInstance(t *testing.T) {
	src := "func Id<T>(x: T) -> T {\n    return x\n}\n\nfunc Use() -> i32 {\n    return Id<i32>(3)\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`@"example.com/app.Id$i32"(i32 %x)`,
		`call i32 @"example.com/app.Id$i32"(i32 3)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "define i32 @\"example.com/app.Id\"(") {
		t.Errorf("the generic declaration was emitted without an instance:\n%s", text)
	}
}

func TestEmitStrEquality(t *testing.T) {
	src := "func Eq(a: str, b: str) -> bool {\n    return a == b\n}\n\nfunc Ne(a: str, b: str) -> bool {\n    return a != b\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"call i32 @memcmp(ptr",
		"icmp eq i64",
		"and i1",
		"declare i32 @memcmp(ptr, ptr, i64)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitAggregateMatchReturn(t *testing.T) {
	src := "func Pick(n: i32) -> str {\n    return match n {\n        0 => \"zero\"\n        _ => \"many\"\n    }\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"@.bytes.",
		"match.end.1:",
		"ret { ptr, i64 }",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitArrayNewPushAndLen(t *testing.T) {
	src := "func Sum() -> i32 {\n    var a: Array<i32> = new Array<i32>(0)\n    a.Push(1)\n    a.Push(2)\n    a.Set(0, 7)\n    var s: i32 = 0\n    for v in a { s += v }\n    return s + a.Get(1) + i32(a.Len())\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"alloca { ptr, i64, i64 }",
		"call ptr @willrt_alloc(i64",
		"arr.full.",
		"call void @willrt_free(ptr",
		"declare ptr @willrt_alloc(i64)",
		"declare void @willrt_free(ptr)",
		"icmp uge i64",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitBuiltinError(t *testing.T) {
	src := "struct E {\n    msg: str\n}\n\n" +
		"func (e: E) Message() -> str {\n    return e.msg\n}\n\n" +
		"func (e: E) Status() -> i32 {\n    return 7\n}\n\n" +
		"func Use() -> i32 {\n    var e: E = E { msg: \"bad\" }\n    var r: Result<i32, Error> = Err(e)\n    return match r {\n        Ok(v) => v\n        Err(x) => x.Status()\n    }\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`%interface.Error = type { ptr, ptr }`,
		"vtable.Error.E",
		"[5 x ptr]",
		"call i32 %t",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitNewRunsDefaultsAndInit(t *testing.T) {
	src := "struct P {\n    x: i32 = 5\n    y: i32\n}\n\n" +
		"func (mut p: P) init(y: i32) {\n    p.y = y\n}\n\n" +
		"func Make() -> P {\n    return new P(7)\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"alloca %\"struct.example.com/app.P\"",
		"store %\"struct.example.com/app.P\" zeroinitializer",
		"store i32 5, ptr",
		"call void @\"example.com/app.P.init\"(ptr %v.new.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitInterfaceValueAndCall(t *testing.T) {
	src := "interface Counter {\n    func Get() -> i32\n}\n\n" +
		"struct C {\n    n: i32\n}\n\n" +
		"func (c: C) Get() -> i32 {\n    return c.n\n}\n\n" +
		"func Use(x: Counter) -> i32 {\n    return x.Get()\n}\n\n" +
		"func main() -> i32 {\n    var c: C = C { n: 3 }\n    return Use(c)\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`%"interface.example.com/app.Counter" = type { ptr, ptr }`,
		`define i32 @"example.com/app.Use"(ptr %x) {`,
		"getelementptr inbounds [4 x ptr], ptr",
		"call i32 %t",
		"store ptr @",
		"vtable.example.com/app.Counter.C",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitOptionAndResultConstructors(t *testing.T) {
	src := "func Find(x: i32) -> Option<i32> {\n    if x > 0 {\n        return Some(x)\n    }\n    return None\n}\n\n" +
		"func UseOpt(x: i32) -> i32 {\n    return match Find(x) {\n        Some(v) => v\n        None => 0\n    }\n}\n\n" +
		"func Divide(x: i32) -> Result<i32, i32> {\n    if x == 0 {\n        return Err(1)\n    }\n    return Ok(x / 2)\n}\n\n" +
		"func UseRes(x: i32) -> Result<i32, i32> {\n    var v: i32 = Divide(x)?\n    return Ok(v + 1)\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define { i32, i32 } @"example.com/app.Find"(i32 %x) {`,
		"store i32 1, ptr", // Some
		"store i32 0, ptr", // None and Ok
		"match.arm.1.0:",
		"try.err.1:",
		"try.ok.1:",
		"ret { i32, i32 }",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "alloca { i32, i32 }") < 4 {
		t.Errorf("a constructor or try slot is missing:\n%s", text)
	}
}

func TestEmitCheckedCasts(t *testing.T) {
	src := "func Widen(a: i32) -> i64 {\n    return i64(a)\n}\n\n" +
		"func Narrow(a: i64) -> i32 {\n    return i32(a)\n}\n\n" +
		"func FromFloat(f: f64) -> i32 {\n    return i32(f)\n}\n\n" +
		"func ToFloat(a: i32) -> f64 {\n    return f64(a)\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"sext i32 %a to i64",
		"icmp slt i64 %a, -2147483648",
		"icmp sgt i64 %a, 2147483647",
		`c"conversion out of range\00"`,
		"fcmp uno double %f, %f",
		`c"cannot convert NaN to integer\00"`,
		"fcmp oge double %f, 2147483648.0",
		"fptosi double %f to i32",
		"sitofp i32 %a to double",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitCheckedArithmeticUsesFixedMessages(t *testing.T) {
	src := "func Mul(a: i32, b: i32) -> i32 {\n    return a * b\n}\n\n" +
		"func Rem(a: i32, b: i32) -> i32 {\n    return a % b\n}\n\n" +
		"func Shr(a: i32, n: i32) -> i32 {\n    return a >> n\n}\n\n" +
		"func Neg(a: i32) -> i32 {\n    return -a\n}\n\n" +
		"func ShiftConst(a: i32) -> i32 {\n    return a << 1\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"call {i32, i1} @llvm.smul.with.overflow.i32(i32 %a, i32 %b)",
		`c"integer overflow\00"`,
		`c"division by zero\00"`,
		`c"shift amount out of range\00"`,
		"icmp eq i32 %b, 0",
		"icmp eq i32 %a, -2147483648",
		"and i1",
		"ashr i32 %a, %n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	// A constant shift in range is checked by the front end, so the lowering
	// does not emit the runtime check for it.
	if strings.Contains(text, "icmp uge i32 1, 32") {
		t.Errorf("a constant shift emitted a runtime check:\n%s", text)
	}
	if strings.Count(text, "shift amount out of range") != 1 {
		t.Errorf("the shift message is not emitted once:\n%s", text)
	}
}

func TestEmitCCall(t *testing.T) {
	src := `import c "stdio.h" {
    fast putchar
}

func Write() -> i32 {
    return c.putchar(65)
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"declare i32 @putchar(i32)",
		"call i32 @putchar(i32 65)",
		"ret i32 %t1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitVoidFunction(t *testing.T) {
	doc := emitWillc(t, "func main() {\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{`define void @"example.com/app.main"() {`, "ret void"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestEmitVoidEntryIsWrapped checks the entry rule: a void entry is not the C
// main, so the generator mangles it and writes an i32 main that returns 0.
func TestEmitVoidEntryIsWrapped(t *testing.T) {
	doc := emitWillc(t, "func Other() {\n}\n\nfunc main() {\n}\n")
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define void @"example.com/app.Other"() {`,
		`define void @"example.com/app.will$main"() {`,
		"define i32 @main() {",
		`call void @"example.com/app.will$main"()`,
		"ret i32 0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "define void @main()") {
		t.Errorf("the void entry kept the C main symbol:\n%s", text)
	}
}

func TestEmitEntryWithoutWrapperWhenNoEntryGiven(t *testing.T) {
	doc := emitWillc(t, "func main() {\n}\n")
	ir, err := EmitWithEntry(doc, "")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if strings.Contains(normalizeIR(string(ir)), "will$main") {
		t.Fatalf("an entry was wrapped without being named:\n%s", ir)
	}
}

func TestEmitKeepsI32Entry(t *testing.T) {
	doc := emitWillc(t, "func main() -> i32 {\n    return 7\n}\n")
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(normalizeIR(string(ir)), "define i32 @main() {") {
		t.Fatalf("IR = %s", ir)
	}
}

func TestEmitBareReturnIsVoid(t *testing.T) {
	doc := emitWillc(t, "func F() {\n    return\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(normalizeIR(string(ir)), `define void @"example.com/app.F"()`) {
		t.Fatalf("IR = %s", ir)
	}
}

// emitWillcIn emits one file with the given packages available, so a source
// that imports a package resolves as it would in a module run.
func emitWillcIn(t *testing.T, file, src, module string, tables map[string][]string) *willc.Doc {
	t.Helper()
	f, ds := parser.Parse(file, []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %d diagnostics", len(ds))
	}
	imports := resolve.Imports{}
	for path, members := range tables {
		exp := resolve.Exported{}
		for _, m := range members {
			exp[m] = &resolve.Symbol{Name: m, Kind: resolve.FuncKind, Exported: true}
		}
		imports[path] = exp
	}
	res, _ := resolve.ResolveWith(f, imports)
	data, err := willc.Emit(f, res, module, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("emit willc: %v", err)
	}
	doc, err := willc.Read(data)
	if err != nil {
		t.Fatalf("read willc: %v", err)
	}
	return doc
}

func TestEmitCrossPackageNewAndInterface(t *testing.T) {
	app := emitWillcIn(t, "main.will",
		"import \"example.com/app/lib\"\n\nfunc Use() -> i32 {\n    var f: lib.Fault = new lib.Fault(\"bad\", 2)\n    var e: Error = f\n    return e.Status()\n}\n\nfunc main() -> i32 {\n    return Use()\n}\n",
		"example.com/app", map[string][]string{"example.com/app/lib": {"Fault"}})
	lib := emitWillcIn(t, "lib.will",
		"struct Fault {\n    message: str\n    status: i32\n}\n\n"+
			"func (f: Fault) init(message: str, status: i32) {\n    f.message = message\n    f.status = status\n}\n\n"+
			"func (f: Fault) Message() -> str {\n    return f.message\n}\n\n"+
			"func (f: Fault) Status() -> i32 {\n    return f.status\n}\n",
		"example.com/app/lib", nil)
	ir, err := EmitModule([]*willc.Doc{app, lib}, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`call void @"example.com/app/lib.Fault.init"(`,
		`@"example.com/app.vtable.Error.example.com/app/lib.Fault"`,
		`ptr @"example.com/app/lib.Fault.Status"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitCrossPackageEnumVariant(t *testing.T) {
	app := emitWillcIn(t, "main.will",
		"import \"example.com/app/lib\"\n\nfunc Use() -> i32 {\n    var x: lib.Level = lib.Level.Warn()\n    _ = x\n    return 0\n}\n\nfunc main() -> i32 {\n    return Use()\n}\n",
		"example.com/app", map[string][]string{"example.com/app/lib": {"Level"}})
	lib := emitWillcIn(t, "lib.will", "enum Level {\n    Debug\n    Info\n    Warn\n}\n", "example.com/app/lib", nil)
	ir, err := EmitModule([]*willc.Doc{app, lib}, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`%"enum.example.com/app/lib.Level" = type { i32 }`,
		`store i32 2`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitModuleResolvesQualifiedCalls(t *testing.T) {
	app := emitWillcIn(t, "main.will",
		"import \"example.com/app/lib\"\n\nfunc main() -> i32 {\n    return lib.Util()\n}\n",
		"example.com/app", map[string][]string{"example.com/app/lib": {"Util"}})
	lib := emitWillcIn(t, "lib.will", "func Util() -> i32 {\n    return 7\n}\n", "example.com/app/lib", nil)
	ir, err := EmitModule([]*willc.Doc{app, lib}, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"define i32 @main() {",
		`call i32 @"example.com/app/lib.Util"()`,
		`define i32 @"example.com/app/lib.Util"() {`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitMethodSignature(t *testing.T) {
	src := "struct Box {\n    v: i64\n}\n\nfunc (b: Box) Ping() {\n}\n\n" +
		"func (b: Box) Answer(n: i32) -> i32 {\n    return n\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define void @"example.com/app.Box.Ping"(ptr %b) {`,
		`define i32 @"example.com/app.Box.Answer"(ptr %b, i32 %n) {`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitAggregateParameterIsABorrow(t *testing.T) {
	doc := emitWillc(t, "struct Box {\n    v: i32\n}\n\nfunc Take(b: Box) {\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(normalizeIR(string(ir)), `define void @"example.com/app.Take"(ptr %b)`) {
		t.Fatalf("IR = %s", ir)
	}
}

func TestEmitIfStatement(t *testing.T) {
	src := "func Sign(n: i32) -> i32 {\n" +
		"    if n < 0 {\n        return 0 - 1\n    } else if n > 0 {\n        return 1\n    }\n" +
		"    return 0\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"icmp slt i32 %n, 0",
		"br i1 %t",
		"if.then.1:",
		"if.else.1:",
		"icmp sgt i32 %n, 0",
		"ret i32 1",
		"ret i32 0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitIfBothBranchesReturn(t *testing.T) {
	src := "func Pick(n: i32) -> i32 {\n" +
		"    if n == 0 {\n        return 1\n    } else {\n        return 2\n    }\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{"icmp eq i32 %n, 0", "ret i32 1", "ret i32 2"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "if.end.1:") {
		t.Errorf("a merge label was written although both branches return:\n%s", text)
	}
	if strings.Contains(text, "unreachable") {
		t.Errorf("the function has an unreachable tail:\n%s", text)
	}
}

func TestEmitLogicalAndNegation(t *testing.T) {
	src := "func F(a: i32, b: i32) -> i32 {\n" +
		"    if a < b && a != 0 {\n        return 0 - a\n    }\n    return ~b\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"icmp slt i32 %a, %b",
		"icmp ne i32 %a, 0",
		"and i1",
		"call {i32, i1} @llvm.ssub.with.overflow.i32(i32 0, i32 %a)",
		"xor i32 %b, -1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitIsDeterministic(t *testing.T) {
	src := "func A(n: i32) -> i32 {\n    return n + 1\n}\n\n" +
		"func B(n: i32) -> i32 {\n    if n > 0 {\n        return A(n)\n    }\n    return 0\n}\n\n" +
		"func C() {\n}\n"
	doc := emitWillc(t, src)
	first, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	for i := 0; i < 8; i++ {
		again, err := Emit(doc)
		if err != nil {
			t.Fatalf("gen: %v", err)
		}
		if !bytes.Equal(again, first) {
			t.Fatalf("two emissions differ:\n--- first\n%s\n--- again\n%s", first, again)
		}
	}
}

func TestEmitLocalsAndAssignment(t *testing.T) {
	src := "func Max(a: i32, b: i32) -> i32 {\n" +
		"    var m: i32 = a\n" +
		"    if b > m {\n        m = b\n    }\n" +
		"    return m\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"%v.m = alloca i32",
		"store i32 %a, ptr %v.m",
		"icmp sgt i32 %b, ",
		"load i32, ptr %v.m",
		"store i32 %b, ptr %v.m",
		"ret i32 %t",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	// The slot is created before the first instruction, so a branch cannot
	// allocate it.
	if strings.Index(text, "%v.m = alloca i32") > strings.Index(text, "store i32 %a, ptr %v.m") {
		t.Errorf("the slot is created after its first use:\n%s", text)
	}
}

func TestEmitCompoundAssignment(t *testing.T) {
	src := "func Sum(n: i32) -> i32 {\n" +
		"    var total: i32 = 0\n" +
		"    total += n\n" +
		"    return total\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{"call {i32, i1} @llvm.sadd.with.overflow.i32", "store i32 %t"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitShadowedLocalsGetTheirOwnSlots(t *testing.T) {
	src := "func F(a: i32) -> i32 {\n" +
		"    var x: i32 = a\n" +
		"    if a > 0 {\n        var x: i32 = 1\n        x += 1\n    }\n" +
		"    return x\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{"%v.x = alloca i32", "%v.x.1 = alloca i32", "ret i32 %t"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitRangeLoop(t *testing.T) {
	src := "func Sum() -> i32 {\n" +
		"    var total: i32 = 0\n" +
		"    for i in Range(0, 3) {\n        total += i\n    }\n" +
		"    return total\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"%v.i = alloca i32",
		"store i32 0, ptr %v.i",
		"for.cond.1:",
		"icmp slt i32",
		"for.body.1:",
		"for.step.1:",
		"add i32",
		"for.end.1:",
		"br label %for.cond.1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitEndlessLoopWithBreak(t *testing.T) {
	src := "func F(n: i32) -> i32 {\n" +
		"    for {\n        if n > 0 {\n            break\n        }\n        return 1\n    }\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{"for.body.1:", "for.end.1:", "br label %for.end.1"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitDeadlineScope(t *testing.T) {
	doc := emitWillc(t, "func F() {\n    deadline 1ms {\n    }\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"call i64 @willrt_task_enter_deadline",
		"call void @willrt_task_exit_deadline",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitStructFieldRead(t *testing.T) {
	src := "struct Point {\n    x: i32\n    y: i32\n}\n\n" +
		"func Sum(p: Point) -> i32 {\n    return p.x + p.y\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"%\"struct.example.com/app.Point\" = type { i32, i32 }",
		"define i32 @\"example.com/app.Sum\"(ptr %p) {",
		"getelementptr inbounds %\"struct.example.com/app.Point\", ptr %p, i32 0, i32 0",
		"getelementptr inbounds %\"struct.example.com/app.Point\", ptr %p, i32 0, i32 1",
		"call {i32, i1} @llvm.sadd.with.overflow.i32",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitAggregateAndFieldWrite(t *testing.T) {
	src := "struct Point {\n    x: i32\n    y: i32\n}\n\n" +
		"func F() -> i32 {\n" +
		"    var p: Point = Point { x: 3, y: 4 }\n" +
		"    p.x = p.x + 1\n" +
		"    return p.x\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"%v.p = alloca %\"struct.example.com/app.Point\"",
		"%v.agg.",
		"store i32 3",
		"store i32 4",
		"load %\"struct.example.com/app.Point\"",
		"store %\"struct.example.com/app.Point\"",
		"store i32 %t",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitNestedField(t *testing.T) {
	src := "struct Inner {\n    v: i32\n}\n\nstruct Outer {\n    inner: Inner\n}\n\n" +
		"func Get(o: Outer) -> i32 {\n    return o.inner.v\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"%\"struct.example.com/app.Inner\" = type { i32 }",
		"%\"struct.example.com/app.Outer\" = type { %\"struct.example.com/app.Inner\" }",
		"getelementptr inbounds %\"struct.example.com/app.Outer\", ptr %o, i32 0, i32 0",
		"getelementptr inbounds %\"struct.example.com/app.Inner\", ptr %t1, i32 0, i32 0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitStructArgumentIsABorrow(t *testing.T) {
	src := "struct Point {\n    x: i32\n}\n\n" +
		"func Take(p: Point) -> i32 {\n    return p.x\n}\n\n" +
		"func F() -> i32 {\n    var q: Point = Point { x: 1 }\n    return Take(q)\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(normalizeIR(string(ir)), "call i32 @\"example.com/app.Take\"(ptr %v.q)") {
		t.Fatalf("the struct argument is not passed as a borrow:\n%s", ir)
	}
}

func TestEmitNestedFixedArrayStructIsEmitted(t *testing.T) {
	src := "struct P {\n    n: i32\n}\n\nstruct Text {\n    body: [2]P\n}\n\nfunc F() -> i32 {\n    return 1\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, `%"struct.example.com/app.Text"`) ||
		!strings.Contains(text, `[2 x %"struct.example.com/app.P"]`) {
		t.Fatalf("the nested fixed-array struct was not emitted:\n%s", ir)
	}
}

func TestEmitNestedFixedArrayFieldIsLowered(t *testing.T) {
	src := "struct P {\n    n: i32\n}\n\nstruct Text {\n    body: [2]P\n}\n\nfunc F(t: Text) -> i32 {\n    return 1\n}\n"
	doc := emitWillc(t, src)
	if _, err := Emit(doc); err != nil {
		t.Fatalf("a parameter whose struct field is a fixed array was rejected: %v", err)
	}
}

func TestEmitI64Function(t *testing.T) {
	doc := emitWillc(t, "func Add(a: i64, b: i64) -> i64 {\n    return a + b\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i64 @"example.com/app.Add"(i64 %a, i64 %b) {`,
		"call {i64, i1} @llvm.sadd.with.overflow.i64(i64 %a, i64 %b)",
		"ret i64"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitUnsignedUsesUnsignedPredicates(t *testing.T) {
	doc := emitWillc(t, "func Below(a: usize, b: usize) -> bool {\n    return a < b\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(normalizeIR(string(ir)), "icmp ult i64 %a, %b") {
		t.Fatalf("a usize comparison is not unsigned:\n%s", ir)
	}
}

func TestEmitSignedUsesSignedPredicates(t *testing.T) {
	doc := emitWillc(t, "func Below(a: isize, b: isize) -> bool {\n    return a < b\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(normalizeIR(string(ir)), "icmp slt i64 %a, %b") {
		t.Fatalf("an isize comparison is not signed:\n%s", ir)
	}
}

func TestEmitShiftFollowsSign(t *testing.T) {
	src := "func Half(n: u64) -> u64 {\n    return n >> 1\n}\n\n" +
		"func HalfSigned(n: i64) -> i64 {\n    return n >> 1\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, "lshr i64 %n, 1") {
		t.Errorf("an unsigned shift is not logical:\n%s", text)
	}
	if !strings.Contains(text, "ashr i64 %n, 1") {
		t.Errorf("a signed shift is not arithmetic:\n%s", text)
	}
}

func TestEmitFloatArithmetic(t *testing.T) {
	doc := emitWillc(t, "func Mid(a: f64, b: f64) -> f64 {\n    return (a + b) / 2.0\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{"fadd double %a, %b", "fdiv double", "ret double"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitStructResultByValue(t *testing.T) {
	src := "struct Point {\n    x: i32\n    y: i32\n}\n\n" +
		"func Make() -> Point {\n    return Point { x: 1, y: 2 }\n}\n\n" +
		"func Use() -> i32 {\n    return Make().x\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define %"struct.example.com/app.Point" @"example.com/app.Make"() {`,
		`ret %"struct.example.com/app.Point" %t`,
		`%v.ret.`,
		`= call %"struct.example.com/app.Point" @"example.com/app.Make"()`,
		`getelementptr inbounds %"struct.example.com/app.Point", ptr %v.ret.`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitStructWithWideFields(t *testing.T) {
	src := "struct Wide {\n    a: i64\n    b: u32\n    c: f64\n}\n\n" +
		"func Get(w: Wide) -> i64 {\n    return w.a\n}\n\n" +
		"func Make() -> i32 {\n" +
		"    var w: Wide = Wide { a: 1, b: 2, c: 3.5 }\n" +
		"    w.a = w.a + 1\n" +
		"    return 0\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`= type { i64, i32, double }`,
		"store i64 1",
		"store i32 2",
		"store double 3.5",
		"load i64, ptr",
		"call {i64, i1} @llvm.sadd.with.overflow.i64",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitArrayIteration(t *testing.T) {
	src := "func Sum(xs: [3]i32) -> i32 {\n" +
		"    var total: i32 = 0\n" +
		"    for i, x in xs {\n" +
		"        total += x\n" +
		"        if i > 2 {\n            break\n        }\n" +
		"    }\n    return total\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.Sum"(ptr %xs) {`,
		"%v.i = alloca i64",
		"icmp ult i64 %t",
		"getelementptr inbounds [3 x i32], ptr %xs, i64 0, i64 %t",
		"load i32, ptr %t",
		"icmp ugt i64",
		"for.step.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	// The element binding is a read-only borrow, so no element slot is made
	// and the element is loaded through the pointer the loop computed.
	if strings.Contains(text, "= alloca i32\n") && strings.Contains(text, "%v.x") {
		t.Errorf("the element binding was copied into a slot:\n%s", text)
	}
	// The array is borrowed for the loop, so its length cannot change and no
	// bound check is emitted. The body's arithmetic is checked on its own.
	if strings.Contains(text, `c"index out of bounds\00"`) {
		t.Errorf("the loop emitted a bound check:\n%s", text)
	}
}

func TestEmitArrayIterationWithOneBinding(t *testing.T) {
	src := "func Sum(xs: [2]i64) -> i64 {\n" +
		"    var total: i64 = 0\n" +
		"    for x in xs {\n        total += x\n    }\n" +
		"    return total\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{"icmp ult i64", "[2 x i64]", "call {i64, i1} @llvm.sadd.with.overflow.i64"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "%v.x") {
		t.Errorf("the element binding was copied into a slot:\n%s", text)
	}
}

func TestEmitFixedArrayOfStrIsLowered(t *testing.T) {
	src := "func Count(xs: [2]str) -> i32 {\n    _ = xs[0].Len()\n    return 0\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("fixed array of str: %v", err)
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, "define i32") || !strings.Contains(text, "ptr %xs") {
		t.Fatalf("the fixed array of str has no layout:\n%s", ir)
	}
}

func TestEmitViewAndArrayIteration(t *testing.T) {
	src := "func Sum(xs: []i32) -> i32 {\n" +
		"    var total: i32 = 0\n" +
		"    for i, x in xs {\n        total += x\n        if i > 100 {\n            break\n        }\n    }\n" +
		"    return total\n}\n\n" +
		"func Count(xs: Array<i32>) -> i64 {\n" +
		"    var n: i64 = 0\n" +
		"    for x in xs {\n        if x > 0 {\n            n += 1\n        }\n    }\n" +
		"    return n\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.Sum"(ptr %xs) {`,
		"getelementptr inbounds { ptr, i64 }, ptr %xs, i32 0, i32 0",
		"getelementptr inbounds { ptr, i64 }, ptr %xs, i32 0, i32 1",
		"icmp ult i64 %t",
		"getelementptr inbounds i32, ptr %t",
		"getelementptr inbounds { ptr, i64, i64 }, ptr %xs, i32 0, i32 1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	// The length is loaded once before the header and the loop borrows the
	// container, so no bound check is emitted. The body's arithmetic is checked
	// on its own.
	if strings.Contains(text, `c"index out of bounds\00"`) {
		t.Errorf("the view loop emitted a bound check:\n%s", text)
	}
}

func TestEmitMethodCallPassesTheReceiver(t *testing.T) {
	src := "struct Counter {\n    n: i32\n}\n\n" +
		"func (c: Counter) Get() -> i32 {\n    return c.n\n}\n\n" +
		"func (mut c: Counter) Add(d: i32) {\n    c.n += d\n}\n\n" +
		"func main() -> i32 {\n    var c: Counter = Counter { n: 7 }\n    c.Add(3)\n    return c.Get()\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.Counter.Get"(ptr %c) {`,
		`define void @"example.com/app.Counter.Add"(ptr %c, i32 %d) {`,
		`call void @"example.com/app.Counter.Add"(ptr %v.c, i32 3)`,
		`call i32 @"example.com/app.Counter.Get"(ptr %v.c)`,
		"call {i32, i1} @llvm.sadd.with.overflow.i32",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitTopLevelInitialization(t *testing.T) {
	doc := emitWillc(t, "var counter: i32 = 40\n\nconst step: i32 = 2\n\n"+
		"func Bump() -> i32 {\n    counter += step\n    return counter\n}\n\n"+
		"func main() -> i32 {\n    return Bump()\n}\n")
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`@"example.com/app.counter" = internal global i32 zeroinitializer`,
		`define void @"example.com/app.will$init"() {`,
		`store i32 40, ptr @"example.com/app.counter"`,
		"call {i32, i1} @llvm.sadd.with.overflow.i32",
		`call void @"example.com/app.will$init"()`,
		`call i32 @"example.com/app.will$main"()`,
		"call void @willrt_shutdown()",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "app.step") {
		t.Errorf("a constant got storage:\n%s", text)
	}
	if strings.Contains(text, "define i32 @example.com/app.will$main") {
		t.Errorf("the entry was not mangled:\n%s", text)
	}
}

func TestEmitPackageInitOrder(t *testing.T) {
	lib := emitWillcAs(t, "var seed: i32 = 5\n\nfunc Unused() -> i32 {\n    return seed\n}\n", "example.com/lib")
	app := emitWillcAs(t, "var start: i32 = 10\n\nfunc main() -> i32 {\n    return start\n}\n", "example.com/app")
	ir, err := EmitModule([]*willc.Doc{lib, app}, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	libInit := strings.Index(text, `call void @"example.com/lib.will$init"()`)
	appInit := strings.Index(text, `call void @"example.com/app.will$init"()`)
	entry := strings.Index(text, `call i32 @"example.com/app.will$main"()`)
	if libInit < 0 || appInit < 0 || entry < 0 {
		t.Fatalf("an init or the entry is missing:\n%s", text)
	}
	if !(libInit < appInit && appInit < entry) {
		t.Fatalf("init order is %d, %d, entry %d:\n%s", libInit, appInit, entry, text)
	}
}

func TestEmitSliceBuildsAView(t *testing.T) {
	src := "func Sum(xs: []i32) -> i32 {\n" +
		"    var total: i32 = 0\n" +
		"    for x in xs {\n        total += x\n    }\n" +
		"    return total\n}\n\n" +
		"func Mid(xs: [4]i32) -> i32 {\n" +
		"    var v: []i32 = xs[Range(1, 3)]\n" +
		"    return Sum(v) + Sum(xs[Range(0, 2)])\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"%v.v = alloca { ptr, i64 }",
		"%v.slice.",
		"getelementptr inbounds [4 x i32], ptr %xs, i64 0, i64",
		"sub i64",
		"store i64",
		`call i32 @"example.com/app.Sum"(ptr %v.v)`,
		`call i32 @"example.com/app.Sum"(ptr %v.slice.`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitLiteralCarriesItsContextType(t *testing.T) {
	// 42 in an i64 position must be recorded as an i64 literal, and the
	// backend must use that type instead of assuming i32.
	doc := emitWillc(t, "func Wide() -> i64 {\n    var n: i64 = 42\n    return n\n}\n")
	if !strings.Contains(willcText(t, doc), `kind=int text="42" type="i64"`) {
		t.Fatalf("the literal type is not recorded:\n%s", willcText(t, doc))
	}
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(normalizeIR(string(ir)), "store i64 42") {
		t.Fatalf("the literal was not stored as i64:\n%s", ir)
	}
}

func TestDeferCapturesArgumentsBeforeBlockExit(t *testing.T) {
	src := "func main() -> i32 {\n" +
		"    for i in Range(0, 3) {\n" +
		"        defer print(i)\n" +
		"    }\n" +
		"    return 0\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define void @"example.com/app.defer.0"(ptr %frame) {`,
		"= call ptr @willrt_alloc(i64",
		`call void @"example.com/app.defer.0"(ptr %t`,
		"getelementptr { i32 }, ptr %frame, i32 0, i32 0",
		"call void @willrt_free(ptr",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	// The print must happen in the thunk, not on the loop body path. If the
	// direct call survived, the loop variable would be read after the loop.
	body := text
	if at := strings.Index(body, `define void @"example.com/app.defer.0"`); at >= 0 {
		body = body[:at]
	}
	if strings.Contains(body, "call void @willrt_print_i64") {
		t.Errorf("the deferred print still runs before the thunk:\n%s", text)
	}
}

func TestSpawnSharesRCHandles(t *testing.T) {
	src := "struct C {\n    n: i32\n}\n\n" +
		"func Read(c: rc<C>) -> i32 {\n    return c.n\n}\n\n" +
		"func main() -> i32 {\n" +
		"    var r: rc<C> = new rc<C>(C { n: 7 })\n" +
		"    var t: Task<i32> = spawn Read(r)\n" +
		"    return t.Join()! - 7\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"call void @willrt_rc_share(i64",
		"declare void @willrt_rc_share(i64)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestSpawnFrameDropReleasesCapturedArray(t *testing.T) {
	src := "func Count(xs: Array<str>) -> i32 {\n" +
		"    return i32(xs.Len())\n" +
		"}\n\n" +
		"func main() -> i32 {\n" +
		"    var xs: Array<str> = new Array<str>(2)\n" +
		"    xs.Push(\"a\")\n" +
		"    var t: Task<i32> = spawn Count(xs)\n" +
		"    return t.Join()!\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"call ptr @willrt_task_new_drop(ptr @",
		"@\"example.com/app.spawn.1.drop\"(ptr %frame)",
		"call void @willrt_free_str(ptr",
		"call void @willrt_free(ptr",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestFixedArrayIndexHasTheElementType(t *testing.T) {
	src := "func First(xs: [16]u8) -> u8 {\n" +
		"    return xs[0]\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"getelementptr inbounds [16 x i8], ptr %xs, i64 0, i64",
		"load i8, ptr",
		"ret i8",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestArrayMethodsLower(t *testing.T) {
	src := "func F(src: []i32) -> i32 {\n" +
		"    var xs: Array<i32> = new Array<i32>(0)\n" +
		"    xs.Append(src)\n" +
		"    xs.Insert(0, 1)\n" +
		"    var removed: i32 = xs.Remove(0)\n" +
		"    var cp: Array<i32> = xs.Clone()\n" +
		"    cp.ShrinkToFit()\n" +
		"    var view: []i32 = cp.Slice(0, cp.Len())\n" +
		"    match cp.Pop() {\n" +
		"        Some(v) => { removed += v }\n" +
		"        None => {}\n" +
		"    }\n" +
		"    return removed + i32(view.Len())\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"append.copy.cond.",
		"insert.cond.",
		"remove.cond.",
		"shrink.alloc.",
		"clone.cond.",
		"call ptr @willrt_alloc(i64",
		"call void @willrt_free(ptr",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestSymbolQuotesALeadingDigit checks the escape used for a package named
// after a directory. LLVM reads an unquoted identifier by its first character,
// so a leading digit has to be quoted even when every character is allowed.
func TestSymbolQuotesALeadingDigit(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"001.will$main", `"001.will$main"`},
		{"example.com/app.will$main", `"example.com/app.will$main"`},
		{"app.will$main", "app.will$main"},
		{"_x1", "_x1"},
	} {
		if got := llvmSymbol(tc.name); got != tc.want {
			t.Errorf("llvmSymbol(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// willcText renders a document for a test message.
func willcText(t *testing.T, doc *willc.Doc) string {
	t.Helper()
	return string(willc.Write(doc))
}
