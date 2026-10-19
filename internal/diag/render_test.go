package diag

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/source"
)

var testBuild = Build{Compiler: "will 0.0", Target: "x86_64-linux-gnu", Opt: "-O2"}

func TestJSONLine(t *testing.T) {
	d := Diagnostic{
		Pos:      source.Pos{File: "test.will", Line: 1, Col: 2},
		End:      source.Pos{File: "test.will", Line: 1, Col: 3},
		Severity: Error,
		Code:     "L0001",
		Message:  "this character is not ASCII",
		Help:     "source files must be ASCII; replace it",
	}
	want := `{"file":"test.will","line":1,"col":2,"end_line":1,"end_col":3,` +
		`"severity":"error","code":"L0001","message":"this character is not ASCII",` +
		`"help":"source files must be ASCII; replace it","notes":[],` +
		`"build":{"compiler":"will 0.0","target":"x86_64-linux-gnu","opt":"-O2"}}`
	if got := JSON(d, testBuild); got != want {
		t.Fatalf("json = %s\nwant  = %s", got, want)
	}
}

func TestJSONAllEndsEveryLine(t *testing.T) {
	ds := []Diagnostic{
		{Pos: source.Pos{File: "a.will", Line: 1, Col: 1}, Severity: Warning, Code: "N0003", Message: "m"},
		{Pos: source.Pos{File: "a.will", Line: 2, Col: 1}, Severity: Error, Code: "T0001", Message: "n"},
	}
	out := JSONAll(ds, testBuild)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], `"severity":"warning"`) || !strings.Contains(lines[0], `"end_line":1`) {
		t.Errorf("line 0 = %s", lines[0])
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("output does not end with a newline")
	}
}

func TestJSONKeepsHTMLAndEscapesQuotes(t *testing.T) {
	d := Diagnostic{
		Pos:      source.Pos{File: "a.will", Line: 3, Col: 4},
		End:      source.Pos{File: "a.will", Line: 3, Col: 5},
		Severity: Error,
		Code:     "S0001",
		Message:  `got "a<b>", want c`,
		Notes:    []Note{{Pos: source.Pos{File: "a.will", Line: 3, Col: 4}, Message: "borrowed here"}},
	}
	got := JSON(d, testBuild)
	if !strings.Contains(got, `"message":"got \"a<b>\", want c"`) {
		t.Errorf("message is not escaped as expected: %s", got)
	}
	if !strings.Contains(got, `"notes":[{"file":"a.will","line":3,"col":4,"message":"borrowed here"}]`) {
		t.Errorf("notes are not rendered: %s", got)
	}
}

func TestHumanFormatRendersNotes(t *testing.T) {
	d := Diagnostic{
		Pos:      source.Pos{File: "a.will", Line: 1, Col: 1},
		Severity: Error,
		Code:     "O0003",
		Message:  "cannot borrow x mutably while it is borrowed",
		Help:     "end the read borrow first",
		Notes:    []Note{{Pos: source.Pos{File: "a.will", Line: 4, Col: 9}, Message: "the read borrow is here"}},
	}
	want := "a.will:1:1: O0003 cannot borrow x mutably while it is borrowed\n" +
		"    help: end the read borrow first\n" +
		"a.will:4:9: note: the read borrow is here\n"
	if got := Format(d); got != want {
		t.Fatalf("format = %q, want %q", got, want)
	}
}
