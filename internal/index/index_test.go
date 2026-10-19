package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/pkg"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const moduleSource = `import "lib"

import c "stdio.h" {
    fast puts
}

interface Shape {
    func Area() -> i32
}

struct Square {
    s: i32
}

func (q: Square) Area() -> i32 {
    return q.s * q.s
}

struct Dot {
    s: i32
}

func Use(x: Shape) -> i32 {
    return x.Area()
}
`

func buildIndex(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "api.will"), moduleSource)
	write(t, filepath.Join(root, "lib", "lib.will"), "func Util() {\n}\n")
	b := pkg.Load(root)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	return string(Build(b))
}

func TestIndexLines(t *testing.T) {
	text := buildIndex(t)
	want := []string{
		`package path="example.com/app"`,
		`package path="example.com/app/lib"`,
		`file path="api.will" package="example.com/app"`,
		`import from="example.com/app" to="lib"`,
		`cfunc header="stdio.h" name="puts" linkage="fast"`,
		`decl file="api.will" line=7 col=1 kind="interface" name="Shape" vis="public"`,
		`decl file="api.will" line=11 col=1 kind="struct" name="Square" vis="public"`,
		`decl file="api.will" line=19 col=1 kind="struct" name="Dot" vis="public"`,
		`decl file="api.will" line=23 col=1 kind="func" name="Use" vis="public"`,
		`method file="api.will" line=15 col=1 type="Square" name="Area" recv="value"`,
		`satisfies type="example.com/app.Square" interface="example.com/app.Shape"`,
		`dispatch file="api.will" line=24 col=12 interface="example.com/app.Shape" targets="example.com/app.Square"`,
	}
	for _, w := range want {
		if !strings.Contains(text, w+"\n") {
			t.Errorf("index lacks %q:\n%s", w, text)
		}
	}
	if strings.Contains(text, `satisfies type="example.com/app.Dot"`) {
		t.Errorf("Dot must not satisfy Shape:\n%s", text)
	}
}

func TestIndexDerivedShow(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "api.will"), "struct P {\n    x: i32\n}\n\nderive Show for P\n")
	text := string(Build(pkg.Load(root)))
	if !strings.Contains(text, `satisfies type="example.com/app.P" interface="Show"`) {
		t.Fatalf("index lacks the derived Show satisfaction:\n%s", text)
	}
}

func TestIndexBuiltinError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	src := `struct E {
    code: i32
}

func (e: E) Message() -> str {
    return "failed"
}

func (e: E) Status() -> i32 {
    return e.code
}
`
	write(t, filepath.Join(root, "api.will"), src)
	text := string(Build(pkg.Load(root)))
	if !strings.Contains(text, `satisfies type="example.com/app.E" interface="Error"`) {
		t.Fatalf("index lacks the Error satisfaction:\n%s", text)
	}
}

func TestIndexIsDeterministic(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "api.will"), moduleSource)
	write(t, filepath.Join(root, "lib", "lib.will"), "func Util() {\n}\n")
	first := string(Build(pkg.Load(root)))
	second := string(Build(pkg.Load(root)))
	if first != second {
		t.Fatalf("two index builds differ:\n--- first\n%s\n--- second\n%s", first, second)
	}
}
