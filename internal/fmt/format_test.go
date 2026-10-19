package fmt

import (
	"bytes"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/willc"
)

func format(t *testing.T, src string) string {
	t.Helper()
	return string(Source([]byte(src)))
}

func TestCanonicalSpacing(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"arithmetic", "func F() {\n    var x: i32 = 1+2*3\n}\n", "func F() {\n    var x: i32 = 1 + 2 * 3\n}\n"},
		{"call and comma", "func F() {\n    G ( 1 ,2 )\n}\n", "func F() {\n    G(1, 2)\n}\n"},
		{"colon and assign", "func F() {\n    var x : i32=1\n}\n", "func F() {\n    var x: i32 = 1\n}\n"},
		{"brace joins header", "func F()\n{\n    G()\n}\n", "func F() {\n    G()\n}\n"},
		{"type arguments keep adjacency", "func F(x: Array<i64>) {\n}\n", "func F(x: Array<i64>) {}\n"},
		{"empty block is one line", "func F() {\n\n}\n", "func F() {}\n"},
		{"nested empty block", "func F() {\n    if x {\n    }\n}\n", "func F() {\n    if x {}\n}\n"},
		{"slice index keeps call spacing", "func F() {\n    var s: []i32 = xs[Range(1, 3)]\n}\n", "func F() {\n    var s: []i32 = xs[Range(1, 3)]\n}\n"},
		{"unary has no space", "func F() {\n    var x: i32 = - a * b\n}\n", "func F() {\n    var x: i32 = -a * b\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := format(t, c.src); got != c.want {
				t.Fatalf("format = %q, want %q", got, c.want)
			}
		})
	}
}

func TestIndentAndBlankLines(t *testing.T) {
	src := "\n\nfunc F() {\n\n\n\n    if x {\n            G()\n    }\n}\n\n\n"
	want := "func F() {\n\n    if x {\n        G()\n    }\n}\n"
	if got := format(t, src); got != want {
		t.Fatalf("format = %q, want %q", got, want)
	}
}

func TestFormatIsIdempotent(t *testing.T) {
	src := "\nfunc F( ) {\n  var x : i32=1+2\n\n\n  if x>0 {\n      G( 1 , 2 )\n  }\n\treturn\n}\n"
	once := format(t, src)
	twice := string(Source([]byte(once)))
	if once != twice {
		t.Fatalf("not idempotent:\n--- once\n%s\n--- twice\n%s", once, twice)
	}
}

func TestFormatKeepsComments(t *testing.T) {
	src := "// header\nfunc F() {\n    // step\n    G() /* tail */\n}\n"
	got := format(t, src)
	for _, want := range []string{"// header", "// step", "/* tail */"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("format dropped %q:\n%s", want, got)
		}
	}
}

func TestFormatKeepsWillc(t *testing.T) {
	src := `func Add(a: i32, b: i32) -> i32 {
    var total: i32 = a+b
    if total > 0 {
        return total*2
    }
    return 0
}
`
	before := dropPositions(emit(t, src))
	formatted := string(Source([]byte(src)))
	after := dropPositions(emit(t, formatted))
	if before != after {
		t.Fatalf("formatting changed the willc:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

// dropPositions removes pos and end fields: formatting moves columns, and the
// rule is that the meaning of the willc does not change.
func dropPositions(data []byte) string {
	var out []string
	for _, line := range bytes.Split(data, []byte("\n")) {
		fields := bytes.Fields(line)
		var kept [][]byte
		for _, f := range fields {
			if bytes.HasPrefix(f, []byte("pos=")) || bytes.HasPrefix(f, []byte("end=")) {
				continue
			}
			kept = append(kept, f)
		}
		out = append(out, string(bytes.Join(kept, []byte(" "))))
	}
	return strings.Join(out, "\n")
}

func emit(t *testing.T, src string) []byte {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %d diagnostics", len(ds))
	}
	res, _ := resolve.Resolve(f)
	data, err := willc.Emit(f, res, "example.com/app", nil, nil, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return data
}

func TestFormatBadSourceIsReturnedUnchanged(t *testing.T) {
	src := "func F() {\n    var x: i32 = \"open\n}\n"
	if got := string(Source([]byte(src))); got != src {
		t.Fatalf("bad source was rewritten: %q", got)
	}
}
