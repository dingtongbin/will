package willc

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/types"
)

var update = flag.Bool("update", false, "rewrite the willc golden files")

func emitSource(t *testing.T, file, src, module string) []byte {
	t.Helper()
	return emitSourceIn(t, file, src, module, nil)
}

// emitSourceIn emits one file with the given packages available, so a source
// that imports a package resolves as it would in a module run.
func emitSourceIn(t *testing.T, file, src, module string, tables map[string][]string) []byte {
	t.Helper()
	f, pds := parser.Parse(file, []byte(src))
	if len(pds) != 0 {
		t.Fatalf("parse diagnostics:\n%s", diag.FormatAll(pds))
	}
	imports := resolve.Imports{}
	for path, members := range tables {
		exp := resolve.Exported{}
		for _, m := range members {
			exp[m] = &resolve.Symbol{Name: m, Kind: resolve.FuncKind, Exported: true}
		}
		imports[path] = exp
	}
	res, rds := resolve.ResolveWith(f, imports)
	if len(rds) != 0 {
		t.Fatalf("resolve diagnostics:\n%s", diag.FormatAll(rds))
	}
	data, err := Emit(f, res, module, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return data
}

// emitCheckedSourceIn runs the type checker before emitting, as the CLI does,
// so literal types such as the usize capacity of a container come from the
// checker instead of the emitter's default.
func emitCheckedSourceIn(t *testing.T, file, src, module string, tables map[string][]string) []byte {
	t.Helper()
	f, pds := parser.Parse(file, []byte(src))
	if len(pds) != 0 {
		t.Fatalf("parse diagnostics:\n%s", diag.FormatAll(pds))
	}
	imports := resolve.Imports{}
	for path, members := range tables {
		exp := resolve.Exported{}
		for _, m := range members {
			exp[m] = &resolve.Symbol{Name: m, Kind: resolve.FuncKind, Exported: true}
		}
		imports[path] = exp
	}
	res, rds := resolve.ResolveWith(f, imports)
	if len(rds) != 0 {
		t.Fatalf("resolve diagnostics:\n%s", diag.FormatAll(rds))
	}
	checked, tds := types.CheckWithFull(f, res, nil)
	if len(tds) != 0 {
		t.Fatalf("type diagnostics:\n%s", diag.FormatAll(tds))
	}
	data, err := Emit(f, res, module, nil, checked.LiteralTypes, checked.CallTypes, checked.NameTypes, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return data
}

func TestEmitCharLiteralIsU32(t *testing.T) {
	// A character is one Unicode scalar value, so its recorded type is u32 even
	// though the source spelling is a quoted character.
	data := emitSource(t, "a.will", "func F() -> u32 {\n    return 'a'\n}\n", "m")
	if !strings.Contains(string(data), `kind=char text="97" type="u32"`) {
		t.Fatalf("the character literal type is not u32:\n%s", data)
	}
}

func TestEmitRoundTrip(t *testing.T) {
	src := `func F(n: i32) -> i32 {
    var x: i32 = n + 1
    if x > 0 {
        return x
    }
    return 0
}
`
	data := emitSource(t, "test.will", src, "example.com/app")
	doc, err := Read(data)
	if err != nil {
		t.Fatalf("read emitted: %v\n%s", err, data)
	}
	if again := Write(doc); !bytes.Equal(again, data) {
		t.Fatalf("round trip differs:\n--- want\n%s\n--- got\n%s", data, again)
	}
}

func TestEmitIsDeterministic(t *testing.T) {
	src := "func F() {\n    Work()\n}\nfunc Work() {}\n"
	a := emitSource(t, "test.will", src, "example.com/app")
	b := emitSource(t, "test.will", src, "example.com/app")
	if !bytes.Equal(a, b) {
		t.Fatalf("two emits differ:\n%s\n%s", a, b)
	}
}

func TestEmitMethodTypeArgs(t *testing.T) {
	// A generic method call carries its type arguments on mcall, the same way
	// a call does. The corpus cannot show this yet because instantiation of
	// generic methods is not modelled by the checker.
	src := "struct Box {\n    v: i64\n}\n\n" +
		"func (b: Box) Get<T>(x: T) -> T {\n    return x\n}\n\n" +
		"func Use(b: Box) -> i64 {\n    return b.Get<i64>(1)\n}\n"
	data := emitSource(t, "test.will", src, "example.com/app")
	if _, err := Read(data); err != nil {
		t.Fatalf("read emitted: %v\n%s", err, data)
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, " mcall ") && strings.Contains(line, "typearg=#") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no mcall with a type argument:\n%s", data)
	}
}

func TestEmitChannelPriorityIsText(t *testing.T) {
	src := "func F() {\n    var ch: chan<i64> = new chan<i64>(4, Priority.High)\n    _ = ch\n}\n"
	data := emitSource(t, "test.will", src, "example.com/app")
	if !strings.Contains(string(data), `prio="High"`) {
		t.Fatalf("channel priority is not text:\n%s", data)
	}
}

func TestEmitVariantPatternsWithoutPayload(t *testing.T) {
	src := "enum Shape {\n    Dot\n    Line\n}\n\n" +
		"func F(s: Shape) -> i64 {\n    return match s {\n        Dot | Line => 1\n    }\n}\n"
	data := emitSource(t, "test.will", src, "example.com/app")
	text := string(data)
	if strings.Count(text, "kind=variant") != 2 {
		t.Fatalf("payloadless variants are not variant patterns:\n%s", text)
	}
	if strings.Contains(text, `text="Dot" kind=bind`) {
		t.Fatalf("a variant name was written as a binding:\n%s", text)
	}
}

func TestEmitReleaseGroups(t *testing.T) {
	src := "func F(n: i32) -> i32 {\n" +
		"    var a: str = \"a\"\n" +
		"    var b: str = \"b\"\n" +
		"    if n > 0 {\n" +
		"        return n\n" +
		"    }\n" +
		"    _ = a?\n" +
		"    _ = b\n" +
		"    return 0\n" +
		"}\n"
	doc := mustRead(t, emitSource(t, "test.will", src, "example.com/app"))
	names := map[int]string{}
	for _, r := range doc.Records {
		if r.Kind == "var" {
			names[r.ID] = testFieldText(r, "text")
		}
	}
	type group struct{ owner, pos string }
	var got []group
	for _, r := range doc.Records {
		if r.Kind != "release" {
			continue
		}
		got = append(got, group{owner: names[testRef(r, "owner")], pos: testFieldText(r, "pos")})
	}
	want := []group{
		// The return inside the if releases every owner in scope, newest first.
		{"b", "test.will:5:9"}, {"a", "test.will:5:9"},
		// A statement with ? may leave, so it gets its own group.
		{"b", "test.will:7:5"}, {"a", "test.will:7:5"},
		// The final return does the same; the block end adds nothing.
		{"b", "test.will:9:5"}, {"a", "test.will:9:5"},
	}
	if len(got) != len(want) {
		t.Fatalf("release groups = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("release %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// mustRead reads an emitted document.
func mustRead(t *testing.T, data []byte) *Doc {
	t.Helper()
	doc, err := Read(data)
	if err != nil {
		t.Fatalf("read emitted: %v\n%s", err, data)
	}
	return doc
}

// testFieldText reads a text field of a record.
func testFieldText(r Record, name string) string {
	for _, f := range r.Fields {
		if f.Name == name && (f.Value.Kind == TextValue || f.Value.Kind == WordValue) {
			return f.Value.Text
		}
	}
	return ""
}

// testRef reads a node reference field of a record.
func testRef(r Record, name string) int {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == RefValue {
			return f.Value.Ref
		}
	}
	return 0
}

func TestEmitGolden(t *testing.T) {
	dir := filepath.Join("..", "..", "tests", "willc", "program")
	src, err := os.ReadFile(filepath.Join(dir, "program.will"))
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	got := emitCheckedSourceIn(t, "program.will", string(src), "example.com/app",
		map[string][]string{"example.com/app/http": {"Status"}})
	golden := filepath.Join(dir, "program.willc.txt")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("willc differs from golden:\n--- want\n%s\n--- got\n%s", want, got)
	}
}

func TestEmitDiagnostics(t *testing.T) {
	f := &ast.File{}
	res := &resolve.Result{Refs: map[ast.Node]*resolve.Symbol{}}
	ds := []diag.Diagnostic{{
		Pos:      source.Pos{File: "a.will", Line: 1, Col: 1},
		End:      source.Pos{File: "a.will", Line: 1, Col: 2},
		Severity: diag.Error,
		Code:     "T0001",
		Message:  "type mismatch",
		Help:     "convert explicitly with T(x)",
	}}
	data, err := Emit(f, res, "x", ds, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if _, err := Read(data); err != nil {
		t.Fatalf("read emitted: %v\n%s", err, data)
	}
	text := string(data)
	if !strings.Contains(text, `#2 diagnostic parent=#1 pos="a.will:1:1" end="a.will:1:2" code="T0001" severity=error message="type mismatch"`) {
		t.Errorf("diagnostic record is wrong:\n%s", text)
	}
	if !strings.Contains(text, `count parent=#1 name="diagnostics" n=1`) {
		t.Errorf("count record is missing:\n%s", text)
	}
}

func TestEmitEmptyFile(t *testing.T) {
	f := &ast.File{}
	res := &resolve.Result{Refs: map[ast.Node]*resolve.Symbol{}}
	data, err := Emit(f, res, "x", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("emit empty file: %v", err)
	}
	if _, err := Read(data); err != nil {
		t.Fatalf("read emitted empty file: %v\n%s", err, data)
	}
}
