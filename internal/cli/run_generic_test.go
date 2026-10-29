package cli

import "testing"

// TestRunGenericClassSourceSurface checks one generic class end to end: a
// constructor, a class-typed field, a method on T, and forced full cycles while
// the instance stays live.
func TestRunGenericClassSourceSurface(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Node {
    value: i32
}

class Box<T> {
    value: T
    tag: i32

    def get(self) -> T {
        return self.value.clone()
    }
}

def main() -> i32 {
    var b: Box<Node> = Box<Node>(value=Node(value=41), tag=1)
    var i: i32 = 0
    while i < 50000 {
        var junk: Node = Node(value=i)
        _ = junk
        i += 1
    }
    var n: Node = b.get()
    return n.value + b.tag
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 42 {
		t.Fatalf("code = %d, want 42, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

// TestRunGenericClassInit checks that an init declared in a generic class is
// specialized and called with the constructor arguments.
func TestRunGenericClassInit(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `class Counter<T> {
    value: T

    def init(self, value: T) {
        self.value = value
    }

    def get(self) -> T {
        return self.value.clone()
    }
}

def main() -> i32 {
    var c: Counter<i32> = Counter<i32>(value=7)
    return c.get()
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 7 {
		t.Fatalf("code = %d, want 7, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}
