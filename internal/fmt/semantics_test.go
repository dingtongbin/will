package fmt

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/willc"
)

// TestFormatKeepsSemantics checks the rule that formatting does not change
// semantics: the willc of the original and of the formatted text must be
// byte-identical once positions are dropped, because formatting moves them.
func TestFormatKeepsSemantics(t *testing.T) {
	src := "func F( a : i32 ) -> i32{\n" +
		" var x:i32= a+1\n" +
		" _=x\n" +
		" return   a\n" +
		"}\n"
	formatted := string(Source([]byte(src)))
	before := stripPositions(t, src)
	after := stripPositions(t, formatted)
	if before != after {
		t.Fatalf("willc changed by formatting:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if formatted == src {
		t.Fatal("the source was already canonical; the test proves nothing")
	}
}

// stripPositions emits willc for src and drops the position fields.
func stripPositions(t *testing.T, src string) string {
	t.Helper()
	f, ds := parser.Parse("a.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %d diagnostics", len(ds))
	}
	res, _ := resolve.Resolve(f)
	data, err := willc.Emit(f, res, "example.com/app", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		var kept []string
		for _, field := range strings.Split(line, " ") {
			if strings.HasPrefix(field, "pos=") || strings.HasPrefix(field, "end=") {
				continue
			}
			kept = append(kept, field)
		}
		out = append(out, strings.Join(kept, " "))
	}
	return strings.Join(out, "\n")
}
