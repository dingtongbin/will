package index

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// goldenIndexText returns the recorded text index that `will index` writes
// for tests/index/app.
func goldenIndexText(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "tests", "index", "app", "index.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden index: %v", err)
	}
	return data
}

func TestParseGoldenIndex(t *testing.T) {
	ix, err := Parse(goldenIndexText(t))
	if err != nil {
		t.Fatalf("parse golden index: %v", err)
	}
	if len(ix.Packages) != 1 || len(ix.Files) != 1 || len(ix.Imports) != 0 {
		t.Errorf("counts: packages=%d files=%d imports=%d", len(ix.Packages), len(ix.Files), len(ix.Imports))
	}
	if len(ix.Decls) != 5 || len(ix.Methods) != 1 || len(ix.Satisfies) != 1 {
		t.Errorf("counts: decls=%d methods=%d satisfies=%d", len(ix.Decls), len(ix.Methods), len(ix.Satisfies))
	}
	if len(ix.Dispatches) != 1 || len(ix.CFuncs) != 0 || len(ix.Targets) != 1 {
		t.Errorf("counts: dispatches=%d cfuncs=%d targets=%d", len(ix.Dispatches), len(ix.CFuncs), len(ix.Targets))
	}

	if pkg, ok := ix.PackageOf("api.will"); !ok || pkg != "example.com/app" {
		t.Errorf("PackageOf(api.will) = %q, %v", pkg, ok)
	}
	if _, ok := ix.PackageOf("missing.will"); ok {
		t.Error("PackageOf(missing.will) reported a package")
	}

	shape, ok := ix.DeclByName("example.com/app", "Shape")
	if !ok || shape.Kind != "interface" || shape.Line != 1 || shape.Vis != "public" || shape.File != "api.will" {
		t.Errorf("DeclByName(Shape) = %+v, %v", shape, ok)
	}
	if _, ok := ix.DeclByName("example.com/app", "Missing"); ok {
		t.Error("DeclByName(Missing) found a declaration")
	}

	area, ok := ix.MethodOf("example.com/app", "Square", "area")
	if !ok || area.Line != 9 || area.Col != 1 || area.Recv != "value" || area.File != "api.will" {
		t.Errorf("MethodOf(Square.area) = %+v, %v", area, ok)
	}
	if _, ok := ix.MethodOf("example.com/app", "Dot", "area"); ok {
		t.Error("MethodOf(Dot.area) found a method")
	}

	if got := ix.ImportsOf("example.com/app"); got != nil {
		t.Errorf("ImportsOf(example.com/app) = %v, want nil", got)
	}

	targets, ok := ix.DispatchTargets(DispatchKey{File: "api.will", Line: 18, Col: 12})
	if !ok || !reflect.DeepEqual(targets, []string{"example.com/app.Square"}) {
		t.Errorf("DispatchTargets(18:12) = %v, %v", targets, ok)
	}
	if _, ok := ix.DispatchTargets(DispatchKey{File: "api.will", Line: 18, Col: 13}); ok {
		t.Error("DispatchTargets(18:13) reported a dispatch")
	}

	if got := ix.InterfacesOf("example.com/app.Square"); !reflect.DeepEqual(got, []string{"example.com/app.Shape"}) {
		t.Errorf("InterfacesOf(Square) = %v", got)
	}
	if got := ix.Implementations("example.com/app.Shape"); !reflect.DeepEqual(got, []string{"example.com/app.Square"}) {
		t.Errorf("Implementations(Shape) = %v", got)
	}
}

func TestParseRoundTrip(t *testing.T) {
	golden := goldenIndexText(t)
	ix, err := Parse(golden)
	if err != nil {
		t.Fatalf("parse golden index: %v", err)
	}
	if got := ix.Write(); !bytes.Equal(got, golden) {
		t.Fatalf("golden round trip differs:\n--- want\n%s\n--- got\n%s", golden, got)
	}

	built := []byte(buildIndex(t))
	ix, err = Parse(built)
	if err != nil {
		t.Fatalf("parse built index: %v\n%s", err, built)
	}
	if got := ix.Write(); !bytes.Equal(got, built) {
		t.Fatalf("built round trip differs:\n--- want\n%s\n--- got\n%s", built, got)
	}
}

func TestDeclAt(t *testing.T) {
	ix, err := Parse(goldenIndexText(t))
	if err != nil {
		t.Fatalf("parse golden index: %v", err)
	}
	square, ok := ix.DeclAt("api.will", 5, 1)
	if !ok || square.Name != "Square" || square.Kind != "struct" || square.Vis != "public" {
		t.Errorf("DeclAt(5:1) = %+v, %v", square, ok)
	}
	area, ok := ix.DeclAt("api.will", 9, 1)
	if !ok || area.Kind != "method" || area.Name != "area" || area.Type != "Square" || area.Recv != "value" {
		t.Errorf("DeclAt(9:1) = %+v, %v", area, ok)
	}
	if _, ok := ix.DeclAt("api.will", 2, 1); ok {
		t.Error("DeclAt(2:1) hit a declaration")
	}
	if _, ok := ix.DeclAt("other.will", 5, 1); ok {
		t.Error("DeclAt(other.will 5:1) hit a declaration")
	}
}

func TestImportsOf(t *testing.T) {
	const text = `package path="example.com/a"
package path="example.com/b"
package path="example.com/c"
file path="a/a.will" package="example.com/a"
file path="a/b.will" package="example.com/a"
import from="example.com/a" to="example.com/b"
import from="example.com/a" to="example.com/c"
import from="example.com/a" to="example.com/b"
import from="example.com/b" to="example.com/c"
`
	ix, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := ix.ImportsOf("example.com/a"); !reflect.DeepEqual(got, []string{"example.com/b", "example.com/c"}) {
		t.Errorf("ImportsOf(a) = %v", got)
	}
	if got := ix.ImportsOf("example.com/b"); !reflect.DeepEqual(got, []string{"example.com/c"}) {
		t.Errorf("ImportsOf(b) = %v", got)
	}
	if got := ix.ImportsOf("example.com/c"); got != nil {
		t.Errorf("ImportsOf(c) = %v, want nil", got)
	}
	got := ix.ImportsOf("example.com/a")
	got[0] = "changed"
	if again := ix.ImportsOf("example.com/a"); again[0] != "example.com/b" {
		t.Errorf("ImportsOf exposes its storage: %v", again)
	}
}

func TestDispatchTargetsEmpty(t *testing.T) {
	const text = `package path="p"
file path="a.will" package="p"
dispatch file="a.will" line=3 col=7 interface="p.Iface" targets=""
`
	ix, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	key := DispatchKey{File: "a.will", Line: 3, Col: 7}
	targets, ok := ix.DispatchTargets(key)
	if !ok || len(targets) != 0 {
		t.Errorf("DispatchTargets(%s) = %v, %v", key, targets, ok)
	}
	dispatch, ok := ix.DispatchAt(key)
	if !ok || dispatch.Interface != "p.Iface" {
		t.Errorf("DispatchAt(%s) = %+v, %v", key, dispatch, ok)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	const declPrefix = "package path=\"p\"\n"
	cases := []struct {
		name string
		text string
		line int
		want string
	}{
		{"empty", "", 1, "the index is empty"},
		{"unknown kind", "bogus path=\"x\"\n", 1, `unknown line kind "bogus"`},
		{"missing field", "file path=\"a.will\"\n", 1, "file needs field package"},
		{"extra field", "package path=\"a\" extra=1\n", 1, `field "extra" is not allowed on "package"`},
		{"out of order", "file package=\"a\" path=\"a.will\"\n", 1, `field "package" is out of order`},
		{"unquoted text", "file path=a.will package=\"a\"\n", 1, "a value is quoted text or an integer"},
		{"unterminated quote", "package path=\"a\n", 1, "the quoted text is not terminated"},
		{"bad escape", "package path=\"a\\q\"\n", 1, "escape the index format does not allow"},
		{"not name=value", "package path\n", 1, "is written name=value"},
		{"leading zero", declPrefix + "decl file=\"a.will\" line=01 col=1 kind=\"func\" name=\"F\" vis=\"public\"\n",
			2, "leading zero"},
		{"integer overflow", declPrefix + "decl file=\"a.will\" line=99999999999999999999 col=1 kind=\"func\" name=\"F\" vis=\"public\"\n",
			2, "is out of range"},
		{"quoted integer", declPrefix + "decl file=\"a.will\" line=\"1\" col=1 kind=\"func\" name=\"F\" vis=\"public\"\n",
			2, `field "line" is an integer, not quoted text`},
		{"zero line", declPrefix + "decl file=\"a.will\" line=0 col=1 kind=\"func\" name=\"F\" vis=\"public\"\n",
			2, "line must be at least 1"},
		{"zero col", declPrefix + "decl file=\"a.will\" line=1 col=0 kind=\"func\" name=\"F\" vis=\"public\"\n",
			2, "col must be at least 1"},
		{"empty name", declPrefix + "decl file=\"a.will\" line=1 col=1 kind=\"func\" name=\"\" vis=\"public\"\n",
			2, "a decl name is never empty"},
		{"bad vis", declPrefix + "decl file=\"a.will\" line=1 col=1 kind=\"func\" name=\"F\" vis=\"internal\"\n",
			2, "it must be public or private"},
		{"bad recv", declPrefix + "method file=\"a.will\" line=1 col=1 type=\"T\" name=\"M\" recv=\"const\"\n",
			2, "it must be value or mut"},
		{"bad linkage", "cfunc header=\"stdio.h\" name=\"puts\" linkage=\"slow\"\n",
			1, "it must be fast, blocking, or plain"},
		{"empty target", "dispatch file=\"a.will\" line=1 col=1 interface=\"p.I\" targets=\"a,,b\"\n",
			1, "the targets field has an empty entry"},
		{"duplicate dispatch", "dispatch file=\"a.will\" line=1 col=1 interface=\"p.I\" targets=\"\"\n" +
			"dispatch file=\"a.will\" line=1 col=1 interface=\"p.I\" targets=\"p.T\"\n",
			2, "two dispatch lines share the position a.will:1:1"},
		{"empty line", "package path=\"a\"\n\nfile path=\"a.will\" package=\"a\"\n", 2, "the line is empty"},
		{"no final newline", "package path=\"a\"", 1, "does not end with a newline"},
		{"cr ending", "package path=\"a\"\r\n", 1, "lines end with LF"},
		{"non ascii", "package path=\"a\xe9\"\n", 1, "the index is not ASCII"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.text))
			if err == nil {
				t.Fatal("Parse accepted a malformed index")
			}
			var perr *Error
			if !errors.As(err, &perr) {
				t.Fatalf("error is %T, want *index.Error", err)
			}
			if perr.Line != tc.line {
				t.Errorf("error line = %d, want %d (%v)", perr.Line, tc.line, err)
			}
			if !strings.HasPrefix(err.Error(), "line "+strconv.Itoa(tc.line)+":") {
				t.Errorf("error text %q does not start with its line", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error text %q does not contain %q", err, tc.want)
			}
		})
	}
}
