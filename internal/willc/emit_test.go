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
)

var update = flag.Bool("update", false, "rewrite the willc golden files")

func emitSource(t *testing.T, file, src, module string) []byte {
	t.Helper()
	f, pds := parser.Parse(file, []byte(src))
	if len(pds) != 0 {
		t.Fatalf("parse diagnostics:\n%s", diag.FormatAll(pds))
	}
	res, rds := resolve.Resolve(f)
	if len(rds) != 0 {
		t.Fatalf("resolve diagnostics:\n%s", diag.FormatAll(rds))
	}
	data, err := Emit(f, res, module, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return data
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

func TestEmitGolden(t *testing.T) {
	dir := filepath.Join("..", "..", "tests", "willc", "program")
	src, err := os.ReadFile(filepath.Join(dir, "program.will"))
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	got := emitSource(t, "program.will", string(src), "example.com/app")
	golden := filepath.Join(dir, "willc.txt")
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
	data, err := Emit(f, res, "x", ds)
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
	data, err := Emit(f, res, "x", nil)
	if err != nil {
		t.Fatalf("emit empty file: %v", err)
	}
	if _, err := Read(data); err != nil {
		t.Fatalf("read emitted empty file: %v\n%s", err, data)
	}
}
