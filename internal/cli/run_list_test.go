package cli

import "testing"

// TestRunListMethods covers the specified list surface end to end: growth,
// insert/remove, clone, reserve, append/slice, pop, for, indexing, builtin len,
// and the callback methods. The program returns 54 when every step agrees: xs is [7,2,3], find(>2) hits 7.
func TestRunListMethods(t *testing.T) {
	src := `def main() -> i32 {
    var xs: list[i32] = list[i32]()
    xs.reserve(8usize)
    xs.push(1)
    xs.push(2)
    xs.push(3)
    xs.insert(0usize, 7)
    var removed: i32 = xs.remove(1usize)
    var ys: list[i32] = xs.clone()
    ys.push(9)
    var view: []i32 = xs.slice(0usize, 3usize)
    var sum: i32 = removed
    for v in view {
        sum += v
    }
    var last: i32 = match ys.pop() {
        Some(v) => v
        None => -1
    }
    var doubled: list[i32] = xs.map(func(v: i32) -> i32 { return v * 2 })
    var evens: list[i32] = xs.filter(func(v: i32) -> bool { return v % 2 == 0 })
    var total: i32 = xs.fold(0, func(a: i32, v: i32) -> i32 { return a + v })
    var found: i32 = match xs.find(func(v: i32) -> bool { return v > 2 }) {
        Some(v) => v
        None => -1
    }
    if not xs.any(func(v: i32) -> bool { return v > 9 }) {
        sum += 1
    }
    if xs.all(func(v: i32) -> bool { return v > 0 }) {
        sum += 1
    }
    return sum + last + i32(len(doubled)) + i32(len(evens)) + total + found + xs[0usize]
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 54 {
		t.Fatalf("run: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestRunListForcedCycles keeps one list alive across many forced collections
// and checks that its data sequence is still reachable.
func TestRunListForcedCycles(t *testing.T) {
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `def main() -> i32 {
    var xs: list[i32] = list[i32](2)
    xs.push(1)
    xs.push(2)
    var i: i32 = 0
    while i < 2500 {
        var tmp: list[i32] = list[i32](4)
        tmp.push(i)
        tmp.push(i + 1)
        i += 1
    }
    return xs[0usize] + i32(xs.len())
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 3 {
		t.Fatalf("run: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}
