package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitWillc(t *testing.T) {
	path := writeFile(t, t.TempDir(), "add.will", goodSource)
	code, out, errOut := runCLI("emit-willc", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if !strings.HasPrefix(out, "#1 module ") {
		t.Fatalf("output does not start with the module record:\n%s", out)
	}
	if !strings.Contains(out, `text="add"`) {
		t.Fatalf("output lacks the function record:\n%s", out)
	}
	if !strings.HasSuffix(out, "end\n") {
		t.Fatalf("output does not end with the end record:\n%s", out)
	}
}

func TestEmitWillcOut(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "add.will", goodSource)
	out := filepath.Join(dir, "add.willc")
	if code, _, errOut := runCLI("emit-willc", "--out", out, path); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if !strings.HasPrefix(string(data), "#1 module ") {
		t.Fatalf("out file does not start with the module record:\n%s", data)
	}
}

func TestEmitWillcRefusesBadProgram(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.will", badSource)
	code, out, _ := runCLI("emit-willc", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "T0001") {
		t.Fatalf("stdout lacks the diagnostic:\n%s", out)
	}
	if strings.Contains(out, "#1 module") {
		t.Fatalf("a bad program still produced willc:\n%s", out)
	}
}

func TestEmitWillcUsage(t *testing.T) {
	if code, _, _ := runCLI("emit-willc"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

// TestEmitLLVMIsDeterministic checks that two cold caches produce the same
// lowering. State-machine frame layout is the part that used to follow map
// iteration order and change between identical builds.
func TestEmitLLVMIsDeterministic(t *testing.T) {
	src := `class Node {
    value: i32
}

class Holder {
    node: Node
    items: Array<i32>
}

async def pause(n: i32) -> i32 {
    return n
}

async def load(n: i32) -> i32 {
    var h: Holder = Holder(node=Node(value=n), items=new Array<i32>(4))
    var x: i32 = await pause(n)
    return h.node.value + x
}

async def main() -> i32 {
    return await load(3)
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "main.will", src)
	t.Setenv("WILL_CACHE", filepath.Join(dir, "cache-a"))
	code, first, errOut := runCLI("emit-llvm", path)
	if code != 0 {
		t.Fatalf("first emit: code = %d, stderr = %q", code, errOut)
	}
	t.Setenv("WILL_CACHE", filepath.Join(dir, "cache-b"))
	code, second, errOut := runCLI("emit-llvm", path)
	if code != 0 {
		t.Fatalf("second emit: code = %d, stderr = %q", code, errOut)
	}
	if first != second {
		t.Fatalf("two identical builds differ:\n--- first\n%s\n--- second\n%s", first, second)
	}
}
