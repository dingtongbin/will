package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunEarlyRcRelease checks that releasing a copy-only rc handle at its
// last use leaves the retained copy alive.
func TestRunEarlyRcRelease(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `struct Counter {
    n: i32
}

func main() -> i32 {
    var first: rc<Counter> = new rc<Counter>(Counter { n: 7 })
    var second: rc<Counter> = first
    _ = second
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestRunEscapingClosureOwnsItsCapture checks that a closure returned from a
// frame clones an owning capture; the source release must not free the value
// the caller's closure still reads.
func TestRunEscapingClosureOwnsItsCapture(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func Make() -> func() -> i32 {
    var text: str = "hello".Clone()
    return func() -> i32 {
        return i32(text.Len())
    }
}

func main() -> i32 {
    var f: func() -> i32 = Make()
    return f()
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 5 {
		t.Fatalf("code = %d, want 5, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunTryMovesThePayload checks that ? copies the payload out, zeros the
// source Option, and lets the caller release the payload once.
func TestRunTryMovesThePayload(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func Take() -> Option<str> {
    var o: Option<str> = Some("abc".Clone())
    var s: str = o?
    return Some(s)
}

func main() -> i32 {
    return match Take() {
        Some(s) => i32(s.Len())
        None => 0
    }
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 3 {
		t.Fatalf("code = %d, want 3, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunUnwrapMovesThePayload checks the same source-zeroing rule for !.
func TestRunUnwrapMovesThePayload(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func Take() -> i32 {
    var o: Option<str> = Some("abcd".Clone())
    var s: str = o!
    return i32(s.Len())
}

func main() -> i32 {
    return Take()
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 4 {
		t.Fatalf("code = %d, want 4, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunFieldMoveZeroesTheSourceField checks that a field moved into a local
// is not also released as part of the dead root struct.
func TestRunFieldMoveZeroesTheSourceField(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `struct Box {
    items: Array<i32>
}

func main() -> i32 {
    var b: Box = Box { items: new Array<i32>(4) }
    b.items.Push(7)
    var items: Array<i32> = b.items
    items.Push(8)
    return i32(items.Len())
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 2 {
		t.Fatalf("code = %d, want 2, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunEmptyInterfaceBox checks that an empty interface local owns its
// scalar box and downcasts without reading a dangling stack slot.
func TestRunEmptyInterfaceBox(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func main() -> i32 {
    var v: interface {} = 7
    return match v.As<i32>() {
        Some(n) => n
        None => 0
    }
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 7 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunInterfaceFieldAndArray checks that interfaces stored in a struct field
// and in an Array are separately boxed and released.
func TestRunInterfaceFieldAndArray(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `struct P {
    s: str
}

func (p: P) Message() -> str {
    return p.s.Clone()
}

func (p: P) Status() -> i32 {
    return 1
}

struct H {
    e: Error
}

func main() -> i32 {
    var e: Error = P { s: "hello" }
    var h: H = H { e: e }
    var xs: Array<Error> = new Array<Error>(2)
    xs.Push(e)
    return i32(h.e.Message().Len()) + i32(xs.Get(0).Message().Len())
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 10 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunInterfaceDowncastClone checks that As<interface> boxes a new concrete
// value instead of sharing the source box.
func TestRunInterfaceDowncastClone(t *testing.T) {
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
    var s: Shape = Square { n: 3 }
    var l: Option<Labeled> = s.As<Labeled>()
    return match l {
        Some(x) => i32(x.Label().Len())
        None => 0
    }
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 6 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestCheckFixedArrayLiteralWrongLength checks the diagnostics path for a
// literal whose element count does not match its type.
func TestCheckFixedArrayLiteralWrongLength(t *testing.T) {
	src := `func main() -> i32 {
    var xs: [2]i32 = [2]i32 { 1 }
    return xs[0]
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, _ := runCLI("check", path)
	if code != 1 {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	if !strings.Contains(out, "T0003 wrong number of array elements") ||
		!strings.Contains(out, "help: write exactly 2 elements") {
		t.Fatalf("stdout = %q", out)
	}
}

// TestRunChannelHandleCopies checks that copying a channel handle retains it
// and that releasing both handles does not free it early.
func TestRunChannelHandleCopies(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func main() -> i32 {
    var ch: chan<str> = new chan<str>(2)
    var ch2: chan<str> = ch
    var sent: Result<(), Error> = wait(ch2.Send("hello"))
    match sent {
        Ok(_) => {}
        Err(_) => {
            return 1
        }
    }
    var got: Option<str> = wait(ch.Recv())
    var n: i32 = match got {
        Some(v) => i32(v.Len())
        None => 0
    }
    ch.Close()
    return n
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 5 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunFixedArrayLiteral checks field and string elements, their owning
// release, and left-to-right construction.
func TestRunFixedArrayLiteral(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `struct S {
    s: str
}

func main() -> i32 {
    var a: str = "ab"
    var xs: [2]str = [2]str { a, "cde" }
    var ps: [2]S = [2]S { S { s: "xy" }, S { s: "z" } }
    return i32(xs[0].Len() + xs[1].Len() + ps[0].s.Len() + ps[1].s.Len())
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 8 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunInterfaceBoxAndClone checks that a stored interface owns a heap box,
// Clone uses the table's clone thunk, and release calls the drop thunk.
func TestRunInterfaceBoxAndClone(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `struct P {
    s: str
}

func (p: P) Message() -> str {
    return p.s.Clone()
}

func (p: P) Status() -> i32 {
    return 1
}

func main() -> i32 {
    var e: Error = P { s: "hello" }
    var f: Error = e.Clone()
    var n: i32 = i32(f.Message().Len())
    return n
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 5 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunSpawnDropsArrayCapture checks that the task frame releases an owned
// array before the runtime frees the frame.
func TestRunSpawnDropsArrayCapture(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func Count(xs: Array<str>) -> i32 {
    return i32(xs.Len())
}

func main() -> i32 {
    var xs: Array<str> = new Array<str>(2)
    xs.Push("a")
    xs.Push("b")
    var t: Task<i32> = spawn Count(xs)
    return wait(t.Join())!
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 2 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestCheckTopLevelWrite checks the O0009 message and help text through the
// command line, not only through the check package.
func TestCheckTopLevelWrite(t *testing.T) {
	src := `var counter: i32 = 0

func main() -> i32 {
    counter = 1
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, _ := runCLI("check", path)
	if code != 1 {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	if !strings.Contains(out, "O0009 top-level counter is published and read-only") ||
		!strings.Contains(out, "help: put the mutable state behind an rc and a guard") {
		t.Fatalf("stdout = %q", out)
	}
}

// TestRunPlaceMoves checks that field and index places move out of a dead local
// root in assignment, return, aggregate, and Option construction positions.
func TestRunPlaceMoves(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `struct S {
    x: str
    y: str
}

struct T {
    x: str
}

func FieldReturn() -> str {
    var s: S = S { x: "abc", y: "de" }
    return s.x
}

func IndexReturn() -> str {
    var xs: Array<str> = new Array<str>(2)
    xs.Push("wxyz")
    return xs[0]
}

func Moves() -> i32 {
    var s: S = S { x: "abc", y: "de" }
    var t: T = T { x: s.x }
    var s2: S = S { x: "fg", y: "hij" }
    var o: Option<str> = Some(s2.y)
    var n: i32 = i32(t.x.Len())
    return n + match o {
        Some(v) => i32(v.Len())
        None => 0
    }
}

func AssignMove() -> i32 {
    var a: S = S { x: "abc", y: "de" }
    var b: S
    b.x = a.x
    b.y = "z"
    return i32(b.x.Len() + b.y.Len())
}

func main() -> i32 {
    return i32(FieldReturn().Len() + IndexReturn().Len()) + Moves() + AssignMove()
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 17 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunNestedEnumAndFixedArrayClone checks that a struct field holding an
// owning enum and a struct field holding an owning fixed array both deep-copy
// and release through the recursive value paths.
func TestRunNestedEnumAndFixedArrayClone(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `enum E {
    Empty
    Text(str)
}

struct H {
    e: E
}

struct F {
    xs: [2]str
}

func TextLen(e: E) -> i32 {
    return match e {
        Text(s) => i32(s.Len())
        Empty => 0
    }
}

func main() -> i32 {
    var h: H = H { e: E.Text("hello") }
    var hc: H = h.Clone()

    var f: F
    f.xs[0] = "ab"
    f.xs[1] = "cde"
    var fc: F = f.Clone()

    var e2: E = E.Text("xy")
    var e3: E = e2.Clone()
    var xs: [2]str
    xs[0] = "z"
    var ys: [2]str = xs.Clone()

    return TextLen(hc.e) + TextLen(e3) + i32(fc.xs[0].Len() + fc.xs[1].Len() + ys[0].Len())
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 13 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunArrayBatchRelease checks that two equal arrays released at one point
// share a plate and free it once.
func TestRunArrayBatchRelease(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func main() -> i32 {
    var a: Array<i32> = new Array<i32>(4)
    var b: Array<i32> = new Array<i32>(4)
    var n: i32 = i32(a.Len() + b.Len())
    return n
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestEmitDecisionsArrayBatch checks that the same group writes one merge
// decision with both release ids.
func TestEmitDecisionsArrayBatch(t *testing.T) {
	src := `func main() -> i32 {
    var a: Array<i32> = new Array<i32>(4)
    var b: Array<i32> = new Array<i32>(4)
    var n: i32 = i32(a.Len() + b.Len())
    return n
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("emit-decisions", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, `merge pos=`) || !strings.Contains(out, `count=2`) || !strings.Contains(out, `reason="same-dominator"`) {
		t.Fatalf("decision record lacks the array merge:\n%s", out)
	}
}

// TestRunBranchMoveReleasesThePathThatStillOwnsTheValue checks the runtime
// zeroing of a moved source: the moved path releases nothing, the other path
// releases the value exactly once.
func TestRunBranchMoveReleasesThePathThatStillOwnsTheValue(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func Take(flag: bool) -> i32 {
    var a: Array<i32> = new Array<i32>(4)
    a.Push(7)
    if flag {
        var b: Array<i32> = a
        b.Push(8)
        return i32(b.Len())
    }
    return i32(a.Len())
}

func main() -> i32 {
    return Take(true) + Take(false)
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 3 {
		t.Fatalf("code = %d, want 3, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestEmitSomeRcRetainsTheHandle checks that storing an rc handle in an Option
// copy retains it; the source owner may still be released later.
func TestEmitSomeRcRetainsTheHandle(t *testing.T) {
	src := `struct C {
    n: i32
}

func main() -> i32 {
    var r: rc<C> = new rc<C>(C { n: 7 })
    var o: Option<rc<C>> = Some(r)
    _ = o
    _ = r
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("emit-llvm", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "willrt_rc_retain") {
		t.Fatalf("Some(rc) did not retain the handle:\n%s", out)
	}
}

// TestEmitEnumReleaseBranches checks that the active enum payload is released,
// not the tag or an inactive variant.
func TestEmitEnumReleaseBranches(t *testing.T) {
	src := `enum E {
    Text(str)
    Number(i32)
}

func main() -> i32 {
    var e: E = E.Text("hello".Clone())
    _ = e
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("emit-llvm", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "enum.rel.") || !strings.Contains(out, "willrt_free_str") {
		t.Fatalf("enum release branch is missing:\n%s", out)
	}
}

// TestRunMovedArrayIsReleasedOnce checks that a value move suppresses the
// source release. Releasing both owners would report a dead block and double
// free the same buffer.
func TestRunMovedArrayIsReleasedOnce(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func main() -> i32 {
    var first: Array<i32> = new Array<i32>(4)
    first.Push(7)
    var second: Array<i32> = first
    second.Push(8)
    return i32(second.Len())
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 2 {
		t.Fatalf("code = %d, want 2, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunContainerPushMovesTheElement checks that Push takes ownership of an
// owning element and the later release frees it exactly once.
func TestRunContainerPushMovesTheElement(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func main() -> i32 {
    var text: str = "abc".Clone()
    var xs: Array<str> = new Array<str>(0)
    xs.Push(text)
    return i32(xs.Get(0).Len())
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 3 {
		t.Fatalf("code = %d, want 3, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunArrayReleaseAtLastSafeUse checks the early release point for a value
// owner whose remaining use is only a length query.
func TestRunArrayReleaseAtLastSafeUse(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `func main() -> i32 {
    var xs: Array<i32> = new Array<i32>(4)
    xs.Push(7)
    var n: usize = xs.Len()
    var later: i32 = 1
    return i32(n) + later
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 2 {
		t.Fatalf("code = %d, want 2, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

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

// TestRunAsyncFrameDropsOwnedLocalsOnPanic checks that a failing async task
// releases the strings and arrays held by its frame before the task boundary
// frees it. A double release fails the allocator check instead of passing.
func TestRunAsyncFrameDropsOwnedLocalsOnPanic(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "asyncframedrop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make module: %v", err)
	}
	writeFile(t, dir, "will.mod", "module example.com/asyncframedrop\nwill 1\n"+
		"require std@v0.1.0\nreplace std@v0.1.0 => "+filepath.ToSlash(filepath.Join(root, "std"))+"\n")
	src := `import "std/time"

async func Worker() -> i32 {
    var text: str = "hello"
    var items: Array<i32> = new Array<i32>(2)
    _ = await time.Sleep(1ms)!
    panic("stop")
    return i32(text.Len()) + i32(items.Len())
}

async func main() -> i32 {
    region {
        var task: Task<i32> = spawn Worker()
        match await task.Join() {
            Ok(_) => {
                return 1
            }
            Err(_) => {}
        }
    }
    return 0
}
`
	writeFile(t, dir, "main.will", src)
	code, out, errOut := runCLI("run", dir)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if !strings.Contains(errOut, "panic: stop") {
		t.Fatalf("stderr = %q, want the child panic report", errOut)
	}
	if strings.Contains(errOut, "free rejected") {
		t.Fatalf("stderr = %q, frame cleanup released a value twice", errOut)
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
