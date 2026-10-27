package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const indexModule = `interface Shape {
    def area() -> i32
}

struct Square {
    s: i32
}

def (q: Square) area() -> i32 {
    return q.s * q.s
}
`

func TestIndexCommand(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	writeModuleFile(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	writeModuleFile(t, filepath.Join(root, "api.will"), indexModule)

	code, out, errOut := runCLI("index", root)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, `package path="example.com/app"`) {
		t.Fatalf("index lacks the package line:\n%s", out)
	}
	if !strings.Contains(out, `satisfies type="example.com/app.Square" interface="example.com/app.Shape"`) {
		t.Fatalf("index lacks the satisfies line:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("index does not end with a newline")
	}
}

func TestIndexCommandOut(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "app")
	writeModuleFile(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	writeModuleFile(t, filepath.Join(root, "api.will"), indexModule)
	out := filepath.Join(dir, "index.txt")
	if code, _, errOut := runCLI("index", "--out", out, root); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if !strings.Contains(string(data), "satisfies") {
		t.Fatalf("out file lacks the satisfies line:\n%s", data)
	}
}

func TestIndexRefusesBrokenModule(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	writeModuleFile(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	writeModuleFile(t, filepath.Join(root, "bad.will"), badSource)
	code, out, _ := runCLI("index", root)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "T0001") {
		t.Fatalf("stdout lacks the diagnostic:\n%s", out)
	}
}

func TestIndexUsage(t *testing.T) {
	if code, _, _ := runCLI("index"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
