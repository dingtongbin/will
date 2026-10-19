package diag

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/source"
)

func TestFormatLongEchoesLineAndCaret(t *testing.T) {
	d := Diagnostic{
		Pos:      source.Pos{File: "a.will", Line: 2, Col: 18},
		Code:     "T0001",
		Message:  "type mismatch: got str, want i32",
		Help:     "convert explicitly with T(x)",
		Severity: Error,
	}
	src := []byte("func F() {\n    var x: i32 = \"s\"\n}\n")
	got := FormatLong(d, src)
	want := "a.will:2:18: T0001 type mismatch: got str, want i32\n" +
		"    help: convert explicitly with T(x)\n" +
		"        var x: i32 = \"s\"\n" +
		"                     ^\n"
	if got != want {
		t.Fatalf("FormatLong =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(Format(d), "var x") {
		t.Fatal("the canonical form echoes source")
	}
}

func TestFormatLongWithoutSource(t *testing.T) {
	d := Diagnostic{Pos: source.Pos{File: "a.will", Line: 9, Col: 1}, Code: "N0001", Message: "undefined name"}
	if got, want := FormatLong(d, []byte("one line\n")), Format(d); got != want {
		t.Fatalf("FormatLong = %q, want the canonical form %q", got, want)
	}
}
