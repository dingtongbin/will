package check

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func freestanding(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	return Freestanding(f)
}

func count(ds []diag.Diagnostic, message string) int {
	n := 0
	for _, d := range ds {
		if d.Code == diag.CodeFreestanding && d.Message == message {
			n++
		}
	}
	return n
}

func TestFreestandingRuntimeUses(t *testing.T) {
	src := "def f() {\n" +
		"    var xs: Array<i64> = new Array<i64>(0)\n" +
		"    var ch: chan<i64> = new chan<i64>(1)\n" +
		"    _ = ch\n" +
		"    _ = xs\n" +
		"    panic(\"boom\")\n" +
		"}\n"
	ds := freestanding(t, src)
	if got := count(ds, runtimeMessage); got != 3 {
		t.Fatalf("runtime uses = %d, want 3 (two new, one panic):\n%s", got, diag.FormatAll(ds))
	}
	d := ds[0]
	if d.Help != runtimeHelp {
		t.Errorf("help = %q, want %q", d.Help, runtimeHelp)
	}
	if d.Pos.Line != 2 {
		t.Errorf("first use at line %d, want 2", d.Pos.Line)
	}
}

func TestFreestandingTasksAndChannels(t *testing.T) {
	src := "def f(ch: chan<i64>) {\n" +
		"    region {\n" +
		"        var t: Task<i64> = spawn G()\n" +
		"        _ = t.join()\n" +
		"    }\n" +
		"    _ = ch.recv()\n" +
		"    ch.send(1)\n" +
		"}\n"
	ds := freestanding(t, src)
	if got := count(ds, runtimeMessage); got != 5 {
		t.Fatalf("runtime uses = %d, want 5 (region, spawn, join, recv, send):\n%s", got, diag.FormatAll(ds))
	}
}

func TestFreestandingImports(t *testing.T) {
	src := "import \"std/math\"\nimport \"std/net\"\n\ndef f() -> f64 {\n    return 0\n}\n"
	ds := freestanding(t, src)
	if got := count(ds, "package std/net is not available with --freestanding"); got != 1 {
		t.Fatalf("package uses = %d, want 1:\n%s", got, diag.FormatAll(ds))
	}
	for _, d := range ds {
		if strings.Contains(d.Message, "std/math") {
			t.Errorf("std/math was rejected: %+v", d)
		}
	}
}

func TestFreestandingAllowsPureCode(t *testing.T) {
	src := "import \"std/math\"\n\nstruct P {\n    x: i32\n}\n\ndef f(p: P) -> i32 {\n    return p.x + 1\n}\n"
	if ds := freestanding(t, src); len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}
