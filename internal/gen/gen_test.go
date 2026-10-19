package gen

import (
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
		"define i32 @Add(i32 %a, i32 %b) {",
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
	for _, want := range []string{"define void @main() {", "ret void"} {
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
		"define void @Other() {",
		"define void @will$main() {",
		"define i32 @main() {",
		"call void @will$main()",
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
	if !strings.Contains(string(ir), "define void @F()") {
		t.Fatalf("IR = %s", ir)
	}
}

func TestEmitUnsupportedNodeIsReported(t *testing.T) {
	doc := emitWillc(t, "func F(a: i32) -> i32 {\n    if a > 0 {\n        return a\n    }\n    return 0\n}\n")
	_, err := Emit(doc)
	if err == nil {
		t.Fatal("an unsupported node was translated")
	}
	if !strings.Contains(err.Error(), "cannot translate node #") {
		t.Fatalf("error = %v, want a G0001 message", err)
	}
}
