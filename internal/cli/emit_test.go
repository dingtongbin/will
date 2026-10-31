package cli

import (
	"fmt"
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

// deterministicSource has an async state machine, a class field and a
// container, so the lowering decisions exercise frame layout and roots.
const deterministicSource = `class Node {
    value: i32
}

class Holder {
    node: Node
    items: list[i32]
}

async def pause(n: i32) -> i32 {
    return n
}

async def load(n: i32) -> i32 {
    var h: Holder = Holder(node=Node(value=n), items=list[i32](4))
    var x: i32 = await pause(n)
    return h.node.value + x
}

async def main() -> i32 {
    return await load(3)
}
`

// TestEmitLLVMIsDeterministic checks that two cold caches produce the same
// lowering. State-machine frame layout is the part that used to follow map
// iteration order and change between identical builds.
func TestEmitLLVMIsDeterministic(t *testing.T) {
	src := deterministicSource
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

// TestReplayEightTimesIsByteIdentical pins the build determinism rule: the
// same input run eight times, each with a cold cache, must produce the same
// willc, LLVM, decisions, and index bytes.
func TestReplayEightTimesIsByteIdentical(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	writeFile(t, root, "will.mod", "module example.com/app\nwill 1\n")
	mainPath := writeFile(t, root, "main.will", deterministicSource)
	commands := []struct {
		name string
		arg  string
	}{
		{"emit-willc", mainPath},
		{"emit-llvm", root},
		{"emit-decisions", root},
		{"index", root},
	}
	for _, c := range commands {
		var first string
		for i := 0; i < 8; i++ {
			t.Setenv("WILL_CACHE", filepath.Join(root, fmt.Sprintf("%s-%d", c.name, i)))
			code, out, errOut := runCLI(c.name, c.arg)
			if code != 0 {
				t.Fatalf("%s run %d: code = %d, stderr = %q", c.name, i, code, errOut)
			}
			if i == 0 {
				first = out
				continue
			}
			if out != first {
				t.Fatalf("%s run %d differs from run 0", c.name, i)
			}
		}
	}
}

// TestEmitDecisionsAndIndexAreDeterministic checks the two replay products the
// gate compares byte for byte. They must not depend on cache state or map
// iteration order.
func TestEmitDecisionsAndIndexAreDeterministic(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	writeFile(t, root, "will.mod", "module example.com/app\nwill 1\n")
	writeFile(t, root, "main.will", deterministicSource)
	for _, command := range []string{"emit-decisions", "index"} {
		t.Setenv("WILL_CACHE", filepath.Join(root, command+"-a"))
		code, first, errOut := runCLI(command, root)
		if code != 0 {
			t.Fatalf("%s first: code = %d, stderr = %q", command, code, errOut)
		}
		t.Setenv("WILL_CACHE", filepath.Join(root, command+"-b"))
		code, second, errOut := runCLI(command, root)
		if code != 0 {
			t.Fatalf("%s second: code = %d, stderr = %q", command, code, errOut)
		}
		if first != second {
			t.Fatalf("%s differs between two cold caches:\n--- first\n%s\n--- second\n%s",
				command, first, second)
		}
	}
}
