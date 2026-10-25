package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDeadlineChildReturnsDeadline checks the cancellation value of an
// awaited child task: a scope deadline is a normal Err(Deadline), not an empty
// panic from an uninitialized task error.
func TestRunDeadlineChildReturnsDeadline(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "deadlinechild")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make module: %v", err)
	}
	writeFile(t, dir, "will.mod", "module example.com/deadlinechild\nwill 1\n"+
		"require std@v0.1.0\nreplace std@v0.1.0 => "+filepath.ToSlash(filepath.Join(root, "std"))+"\n")
	src := `import "std/time"

async func Nap() -> Result<(), Error> {
    return await time.Sleep(50ms)
}

async func Wait() -> Option<i32> {
    match await time.Sleep(50ms) {
        Ok(_) => {
            return Some(1)
        }
        Err(_) => {
            return None
        }
    }
}

async func main() -> i32 {
    var saw: bool = false
    deadline 1ms {
        var result: Result<(), Error> = await Nap()
        match result {
            Err(e) => {
                if e.Status() != 9 {
                    return 1
                }
                saw = true
            }
            Ok(_) => {
                return 2
            }
        }
    }
    if !saw {
        return 3
    }
    var none: bool = false
    deadline 1ms {
        var item: Option<i32> = await Wait()
        match item {
            None => {
                none = true
            }
            Some(_) => {
                return 4
            }
        }
    }
    if !none {
        return 5
    }
    return 0
}
`
	writeFile(t, dir, "main.will", src)
	code, out, errOut := runCLI("run", dir)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestRunJoinReturnsTheStoredValue runs a real program through the same path
// as will run. A join keeps its error copy in a task frame, and the status word
// after that copy must not touch the joined value.
func TestRunJoinReturnsTheStoredValue(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func Work() -> i64 {
    return 1234567890123i64
}

async func main() -> i32 {
    region {
        var t: Task<i64> = spawn Work()
        var v: i64 = await t.Join()!
        print("v=", v, "\n")
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if !strings.Contains(out, "v=1234567890123\n") {
		t.Fatalf("stdout = %q", out)
	}
}

// TestRunInterfaceDowncasts checks the type identity word at run time. The
// concrete downcast returns a value; the interface downcast builds a second
// table over the same data pointer.
func TestRunInterfaceDowncasts(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `interface Shape {
    func Area() -> i32
}

interface Labeled {
    func Label() -> str
}

struct Square {
    n: i32
}

func (s: Square) Area() -> i32 {
    return s.n
}

func (s: Square) Label() -> str {
    return "square"
}

func main() -> i32 {
    var shape: Shape = Square { n: 12 }
    if !shape.Is<Square>() {
        return 1
    }
    var square: Option<Square> = shape.As<Square>()
    match square {
        Some(value) => {
            if value.n != 12 {
                return 2
            }
        }
        None => {
            return 3
        }
    }
    var labeled: Option<Labeled> = shape.As<Labeled>()
    match labeled {
        Some(value) => {
            if value.Label() != "square" {
                return 4
            }
        }
        None => {
            return 5
        }
    }
    var empty: Shape
    if empty.Is<Square>() {
        return 6
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestRunUnitEnumEquality checks that a payload-free enum compares by its tag
// even when one side is a qualified enum value from another package.
func TestRunUnitEnumEquality(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `enum Choice {
    A
    B
    C
}

func Pick() -> Choice {
    return B
}

func main() -> i32 {
    if Pick() != B {
        return 1
    }
    if Pick() == C {
        return 2
    }
    var choice: Choice = B
    if choice != B {
        return 3
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestRunEmptyInterface checks the anonymous interface at run time: any value
// satisfies it, and only Is/As can recover the concrete type.
func TestRunEmptyInterface(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func Number(value: interface {}) -> i32 {
    var got: Option<i32> = value.As<i32>()
    match got {
        Some(number) => {
            return number
        }
        None => {
            return -1
        }
    }
}

func main() -> i32 {
    var number: interface {} = 42
    if !number.Is<i32>() {
        return 1
    }
    if Number(number) != 42 {
        return 2
    }
    if number.Is<str>() {
        return 3
    }
    var text: interface {} = "will"
    if !text.Is<str>() {
        return 4
    }
    var empty: interface {}
    if empty.Is<i32>() {
        return 5
    }
    var same: Option<interface {}> = number.As<interface {}>()
    match same {
        Some(value) => {
            if !value.Is<i32>() {
                return 6
            }
        }
        None => {
            return 7
        }
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestRunMutableScalarParameters checks that a mutable scalar is a pointer in
// both the ordinary call ABI and the async task ABI.
func TestRunMutableScalarParameters(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func Inc(mut value: i32) -> i32 {
    value += 1
    return value
}

async func Add(mut value: i32) -> i32 {
    value += 2
    return value
}

async func main() -> i32 {
    var first: i32 = 1
    var inc: i32 = Inc(first)
    var second: i32 = 4
    var add: i32 = await Add(second)
    print(first, " ", inc, " ", second, " ", add, "\n")
    if first != 2 || inc != 2 || second != 6 || add != 6 {
        return 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if !strings.Contains(out, "2 2 6 6\n") {
		t.Fatalf("stdout = %q", out)
	}
}

// TestRunAsyncMutParameters checks that a task frame keeps the caller's
// pointer for a mutable receiver and a mutable Array parameter.
func TestRunAsyncMutParameters(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `struct Counter {
    n: i32
}

async func (mut c: Counter) Add(value: i32) -> i32 {
    c.n += value
    return c.n
}

async func Grow(mut values: Array<i32>) -> i32 {
    values.Push(7)
    return i32(values.Len())
}

async func main() -> i32 {
    var counter: Counter = Counter { n: 1 }
    var added: i32 = await counter.Add(4)
    var values: Array<i32> = new Array<i32>(0)
    var grown: i32 = await Grow(values)
    print(counter.n, " ", values.Len(), "\n")
    if counter.n != 5 || added != 5 || grown != 1 || values.Len() != 1 {
        return 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if !strings.Contains(out, "5 1\n") {
		t.Fatalf("stdout = %q", out)
	}
}

// TestRunMPMCCountsEveryMessage moves values across lanes through one channel
// with two producers and two consumers. The result crosses two task boundaries,
// so a truncated or duplicated join value changes the sum.
func TestRunMPMCCountsEveryMessage(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `lane L0 struct L0Marker {
}

lane L1 struct L1Marker {
}

lane L2 struct L2Marker {
}

lane L3 struct L3Marker {
}

async func Produce(ch: chan<i32>, n: i32) {
    for i in Range(0, n) {
        _ = await ch.Send(i)
    }
}

async func Consume(ch: chan<i32>) -> i64 {
    var sum: i64 = 0
    for {
        var item: Option<i32> = await ch.Recv()
        match item {
            Some(value) => {
                sum += i64(value)
            }
            None => {
                break
            }
        }
    }
    return sum
}

async func main() -> i32 {
    var ch: chan<i32> = new chan<i32>(64)
    var total: i64 = 0
    region {
        var p1: Task<()> = spawn on L0 Produce(ch, 20000)
        var p2: Task<()> = spawn on L1 Produce(ch, 20000)
        var c1: Task<i64> = spawn on L2 Consume(ch)
        var c2: Task<i64> = spawn on L3 Consume(ch)
        _ = await p1.Join()!
        _ = await p2.Join()!
        ch.Close()
        var c1v: i64 = await c1.Join()!
        var c2v: i64 = await c2.Join()!
        total = c1v + c2v
    }
    print("total=", total, "\n")
    if total != 399980000i64 {
        return 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if !strings.Contains(out, "total=399980000\n") {
		t.Fatalf("stdout = %q", out)
	}
}
