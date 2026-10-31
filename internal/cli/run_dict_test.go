package cli

import (
	"strings"
	"testing"
)

// TestRunDictScalarKeys covers dict construction, set, get, has, delete, len,
// keys, and values with scalar keys.
func TestRunDictScalarKeys(t *testing.T) {
	src := `def main() -> i32 {
    var d: dict[i32, i32] = dict[i32, i32](4)
    d.set(1, 10)
    d.set(2, 20)
    d.set(1, 11)
    var score: i32 = 0
    if d.has(2) {
        score += 1
    }
    if d.delete(2) {
        score += 2
    }
    if not d.has(2) {
        score += 4
    }
    if len(d) == 1 {
        score += 8
    }
    var keys: list[i32] = d.keys()
    var values: list[i32] = d.values()
    var total: i32 = 0
    for v in values {
        total += v
    }
    var one: i32 = match d.get(1) {
        Some(v) => v
        None => 0
    }
    return score + total + i32(len(keys)) + one
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 38 {
		t.Fatalf("run: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestRunDictStrKeys covers dict with str keys end to end.
func TestRunDictStrKeys(t *testing.T) {
	src := `def main() -> i32 {
    var d: dict[str, i32] = dict[str, i32](4)
    d.set("aa", 1)
    d.set("bb", 2)
    d.set("aa", 11)
    var a: i32 = match d.get("aa") {
        Some(v) => v
        None => 0
    }
    if not d.has("cc") {
        a += 1
    }
    return a + i32(d.len())
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 14 {
		t.Fatalf("run: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestRunSetAddHasDelete covers set construction, add, has, delete, and len.
func TestRunSetAddHasDelete(t *testing.T) {
	src := `def main() -> i32 {
    var s: set[i32] = set[i32](4)
    if not s.add(3) {
        return 1
    }
    if s.add(3) {
        return 2
    }
    if not s.has(3) {
        return 3
    }
    if not s.delete(3) {
        return 4
    }
    if s.has(3) {
        return 5
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("run: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestRunDictSetEach checks both each callbacks visit every live entry.
func TestRunDictSetEach(t *testing.T) {
	src := `def main() -> i32 {
    var d: dict[i32, i32] = dict[i32, i32](4)
    d.set(1, 10)
    d.set(2, 20)
    d.each(func(k: i32, v: i32) {
        print(k, "=", v, "\n")
    })
    var s: set[i32] = set[i32](4)
    s.add(3)
    s.add(4)
    s.each(func(v: i32) {
        print("v=", v, "\n")
    })
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("run: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	for _, want := range []string{"1=10", "2=20", "v=3", "v=4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, missing %q", out, want)
		}
	}
}
