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

func emitWillc(t *testing.T, src string) *willc.Doc {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %d diagnostics", len(ds))
	}
	res, _ := resolve.Resolve(f)
	checked, _ := types.CheckFull(f, res)
	data, err := willc.Emit(f, res, "example.com/app", nil, checked.LiteralTypes)
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
	text := string(ir)
	for _, want := range []string{
		`define i32 @"example.com/app.Add"(i32 %a, i32 %b) {`,
		"entry:",
		"add i32 %a, %b",
		"ret i32 %t1",
		"}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
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
	text := string(ir)
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
	text := string(ir)
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
	text := string(ir)
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
	if strings.Contains(string(ir), "will$main") {
		t.Fatalf("an entry was wrapped without being named:\n%s", ir)
	}
}

func TestEmitKeepsI32Entry(t *testing.T) {
	doc := emitWillc(t, "func main() -> i32 {\n    return 7\n}\n")
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(string(ir), "define i32 @main() {") {
		t.Fatalf("IR = %s", ir)
	}
}

func TestEmitBareReturnIsVoid(t *testing.T) {
	doc := emitWillc(t, "func F() {\n    return\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(string(ir), `define void @"example.com/app.F"()`) {
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
	data, err := willc.Emit(f, res, module, nil, nil)
	if err != nil {
		t.Fatalf("emit willc: %v", err)
	}
	doc, err := willc.Read(data)
	if err != nil {
		t.Fatalf("read willc: %v", err)
	}
	return doc
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
	text := string(ir)
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
	text := string(ir)
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
	if !strings.Contains(string(ir), `define void @"example.com/app.Take"(ptr %b)`) {
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
	text := string(ir)
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
	text := string(ir)
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
	text := string(ir)
	for _, want := range []string{
		"icmp slt i32 %a, %b",
		"icmp ne i32 %a, 0",
		"and i1",
		"sub i32 0, %a",
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
	text := string(ir)
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
	text := string(ir)
	for _, want := range []string{"add i32 %t", "store i32 %t"} {
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
	text := string(ir)
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
	text := string(ir)
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
	text := string(ir)
	for _, want := range []string{"for.body.1:", "for.end.1:", "br label %for.end.1"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestEmitUnsupportedNodeIsReported(t *testing.T) {
	// A string iteration needs the UTF-8 decoder, which is not lowered yet.
	doc := emitWillc(t, "func F(s: str) -> i32 {\n    var n: i32 = 0\n    for x in s {\n        n += 1\n    }\n    return n\n}\n")
	_, err := Emit(doc)
	if err == nil {
		t.Fatal("an unsupported node was translated")
	}
	if !strings.Contains(err.Error(), "cannot translate node #") {
		t.Fatalf("error = %v, want a G0001 message", err)
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
	text := string(ir)
	for _, want := range []string{
		"%\"struct.example.com/app.Point\" = type { i32, i32 }",
		"define i32 @\"example.com/app.Sum\"(ptr %p) {",
		"getelementptr inbounds %\"struct.example.com/app.Point\", ptr %p, i32 0, i32 0",
		"getelementptr inbounds %\"struct.example.com/app.Point\", ptr %p, i32 0, i32 1",
		"add i32",
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
	text := string(ir)
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
	text := string(ir)
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
	if !strings.Contains(string(ir), "call i32 @\"example.com/app.Take\"(ptr %v.q)") {
		t.Fatalf("the struct argument is not passed as a borrow:\n%s", ir)
	}
}

func TestEmitUnusedUnloweredStructIsIgnored(t *testing.T) {
	src := "struct Text {\n    body: str\n}\n\nfunc F() -> i32 {\n    return 1\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if strings.Contains(string(ir), "%\"struct.example.com/app.Text\"") {
		t.Fatalf("a struct whose field is not lowered was emitted:\n%s", ir)
	}
}

func TestEmitUnloweredFieldIsReported(t *testing.T) {
	src := "struct Text {\n    body: str\n}\n\nfunc F(t: Text) -> i32 {\n    return 1\n}\n"
	doc := emitWillc(t, src)
	if _, err := Emit(doc); err == nil {
		t.Fatal("a parameter whose struct field is not lowered was translated")
	}
}

func TestEmitI64Function(t *testing.T) {
	doc := emitWillc(t, "func Add(a: i64, b: i64) -> i64 {\n    return a + b\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		`define i64 @"example.com/app.Add"(i64 %a, i64 %b) {`,
		"add i64 %a, %b",
		"ret i64",
	} {
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
	if !strings.Contains(string(ir), "icmp ult i64 %a, %b") {
		t.Fatalf("a usize comparison is not unsigned:\n%s", ir)
	}
}

func TestEmitSignedUsesSignedPredicates(t *testing.T) {
	doc := emitWillc(t, "func Below(a: isize, b: isize) -> bool {\n    return a < b\n}\n")
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.Contains(string(ir), "icmp slt i64 %a, %b") {
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
	text := string(ir)
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
	text := string(ir)
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
	text := string(ir)
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
	text := string(ir)
	for _, want := range []string{
		`= type { i64, i32, double }`,
		"store i64 1",
		"store i32 2",
		"store double 3.5",
		"load i64, ptr",
		"add i64",
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
	text := string(ir)
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
	// bound check is emitted.
	if strings.Contains(text, "willrt_panic") {
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
	text := string(ir)
	for _, want := range []string{"icmp ult i64", "[2 x i64]", "add i64"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "%v.x") {
		t.Errorf("the element binding was copied into a slot:\n%s", text)
	}
}

func TestEmitArrayIterationRejectsUnsupportedElements(t *testing.T) {
	// An array of str is not lowered yet; the loop must say so instead of
	// emitting a load of an unsupported type.
	src := "func Count(xs: [2]str) -> i32 {\n    return 0\n}\n"
	doc := emitWillc(t, src)
	if _, err := Emit(doc); err == nil {
		t.Fatal("an array of str was lowered")
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
	text := string(ir)
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
	// container, so no bound check is emitted.
	if strings.Contains(text, "willrt_panic") {
		t.Errorf("the view loop emitted a bound check:\n%s", text)
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
	if !strings.Contains(string(ir), "store i64 42") {
		t.Fatalf("the literal was not stored as i64:\n%s", ir)
	}
}

// willcText renders a document for a test message.
func willcText(t *testing.T, doc *willc.Doc) string {
	t.Helper()
	return string(willc.Write(doc))
}
