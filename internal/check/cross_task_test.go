package check

import (
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/types"
)

func crossDiagnostics(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %v", ds)
	}
	res, ds := resolve.Resolve(f)
	if len(ds) != 0 {
		t.Fatalf("resolve: %v", ds)
	}
	checked, ds := types.CheckWithFull(f, res, nil)
	if len(ds) != 0 {
		t.Fatalf("types: %v", ds)
	}
	return CrossTask(f, checked)
}
