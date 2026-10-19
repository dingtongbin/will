package gen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/willc"
)

func emitWillc(t *testing.T, src string) *willc.Doc {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %d diagnostics", len(ds))
	}
	res, _ := resolve.Resolve(f)
	data, err := willc.Emit(f, res, "example.com/app", nil)
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
	data, err := willc.Emit(f, res, module, nil)
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
	doc := emitWillc(t, "struct Box {\n    v: i64\n}\n\nfunc Take(b: Box) {\n}\n")
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

func TestEmitUnsupportedNodeIsReported(t *testing.T) {
	doc := emitWillc(t, "func F(a: i32) -> i32 {\n    var x: i32 = a\n    return x\n}\n")
	_, err := Emit(doc)
	if err == nil {
		t.Fatal("an unsupported node was translated")
	}
	if !strings.Contains(err.Error(), "cannot translate node #") {
		t.Fatalf("error = %v, want a G0001 message", err)
	}
}
