package check

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func TestLeakWarnsAtTheCall(t *testing.T) {
	src := `def f(xs: Array<i32>) {
    leak(xs)
}
`
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %s", diag.FormatAll(ds))
	}
	got := Leaks(f)
	if len(got) != 1 {
		t.Fatalf("diagnostics = %d, want one:\n%s", len(got), diag.FormatAll(got))
	}
	if got[0].Code != diag.CodeExplicitLeak || got[0].Severity != diag.Warning {
		t.Fatalf("diagnostic = %+v", got[0])
	}
	if got[0].Message != "this value is explicitly leaked" ||
		got[0].Help != "it will not be released" {
		t.Fatalf("message/help = %q / %q", got[0].Message, got[0].Help)
	}
}
