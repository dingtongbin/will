package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunClassSourceSurface checks the final class syntax end to end: fields,
// init, a method, a no-init constructor, and the GC lowering behind them.
func TestRunClassSourceSurface(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `class Node {
    value: i32

    def init(self, value: i32) {
        self.value = value
    }

    def get(self) -> i32 {
        return self.value + 1
    }
}

class Point {
    x: i32
    y: i32 = 2
}

def main() -> i32 {
    var p: Point = Point(x=4)
    var n: Node = Node(6)
    if p.x != 4 or p.y != 2 {
        return 11
    }
    return n.get()
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

func TestRunClassContainerRoots(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Node {
    value: i32

    def init(self, value: i32) {
        self.value = value
    }

    def get(self) -> i32 {
        return self.value
    }
}

class Box {
    items: Array<Node>

    def init(self) {
        self.items = new Array<Node>(0)
    }
}

def main() -> i32 {
    var box: Box = Box()
    var i: i32 = 0
    while i < 5000 {
        box.items.push(Node(i))
        i = i + 1
    }
    var total: i32 = 0
    var j: i32 = 0
    while j < 5000 {
        var node: Node = box.items.get(usize(j))
        total = total + node.get()
        j = j + 1
    }
    if total != 12497500 {
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
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunClassFieldContainers(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Box {
    values: Map<i32, i32>
    seen: Set<i32>

    def init(self) {
        self.values = new Map<i32, i32>(8)
        self.seen = new Set<i32>(8)
    }
}

def main() -> i32 {
    var round: i32 = 0
    while round < 40 {
        var box: Box = Box()
        var i: i32 = 0
        while i < 300 {
            box.values.set(i, i + 1)
            box.seen.add(i)
            i += 1
        }
        var total: i32 = 0
        var j: i32 = 0
        while j < 300 {
            match box.values.get(j) {
                Some(v) => {
                    total += v
                }
                None => {
                    return 10
                }
            }
            if not box.seen.has(j) {
                return 11
            }
            j += 1
        }
        if total != 45150 {
            return 12
        }
        round += 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunClassArrayAppendAndMutate(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Box {
    items: Array<i32>

    def init(self) {
        self.items = new Array<i32>(2)
    }
}

def main() -> i32 {
    var round: i32 = 0
    while round < 60 {
        var box: Box = Box()
        var i: i32 = 0
        while i < 200 {
            box.items.push(i)
            i += 1
        }
        var view: []i32 = box.items.slice(0, 200)
        box.items.append(view)
        if box.items.len() != 400 {
            return 1000 + round
        }
        box.items.insert(usize(0), 99)
        if box.items.len() != 401 or box.items.get(0) != 99 {
            return 2000 + round
        }
        if box.items.remove(usize(0)) != 99 {
            return 3000 + round
        }
        box.items.shrink_to_fit()
        if box.items.len() != 400 {
            return 4000 + round
        }
        round += 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunClassContainerMillion(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Box {
    values: Map<i32, i32>
    seen: Set<i32>

    def init(self) {
        self.values = new Map<i32, i32>(8)
        self.seen = new Set<i32>(8)
    }
}

def main() -> i32 {
    var round: i32 = 0
    while round < 1000 {
        var box: Box = Box()
        var i: i32 = 0
        while i < 500 {
            box.values.set(i, i + 1)
            box.seen.add(i)
            i += 1
        }
        if box.values.len() != 500 or box.seen.len() != 500 {
            return 3000 + round
        }
        var j: i32 = 0
        while j < 500 {
            match box.values.get(j) {
                Some(v) => {
                    if v != j + 1 {
                        return 5000 + j
                    }
                }
                None => {
                    return 6000 + j
                }
            }
            if not box.seen.has(j) {
                return 7000 + j
            }
            j += 1
        }
        round += 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunClassStrLifetime(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class TextBox {
    text: str

    def init(self) {
        self.text = "seed"
    }

    def replace(self, round: i32) {
        self.text = "value=" + str(round) + ":tail"
    }
}

def main() -> i32 {
    var round: i32 = 0
    while round < 120 {
        var box: TextBox = TextBox()
        box.replace(round)
        if box.text.len() < 8 {
            return 1000 + round
        }
        var copy: str = box.text.clone()
        if copy.len() != box.text.len() {
            return 2000 + round
        }
        var view: []u8 = box.text[0:2]
        if view.len() != 2 or view.get(0) != 118u8 {
            return 2500 + round
        }
        var i: i32 = 0
        while i < 100 {
            var tmp: str = "head" + str(i)
            if tmp.len() < 4 {
                return 3000 + round
            }
            i += 1
        }
        round += 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunClassInlineAggregateRoot(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Node {
    value: i32

    def init(self, value: i32) {
        self.value = value
    }
}

struct Pair {
    node: Node
    label: str
}

class Holder {
    pair: Pair

    def init(self, value: i32) {
        self.pair = Pair(node=Node(value), label="node=" + str(value))
    }
}

def main() -> i32 {
    var round: i32 = 0
    while round < 200 {
        var holder: Holder = Holder(round)
        if holder.pair.node.value != round {
            return 1000 + round
        }
        if holder.pair.label.len() < 5 {
            return 2000 + round
        }
        var text: str = holder.pair.label.clone()
        if text.len() != holder.pair.label.len() {
            return 3000 + round
        }
        round += 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunArrayOfStrRoot(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `def main() -> i32 {
    var round: i32 = 0
    while round < 120 {
        var xs: Array<str> = new Array<str>(2)
        var i: i32 = 0
        while i < 80 {
            xs.push("v=" + str(i))
            i += 1
        }
        if xs.len() != 80 {
            return 1000 + round
        }
        var j: i32 = 0
        while j < 80 {
            if xs.get(usize(j)).len() < 2 {
                return 2000 + round
            }
            j += 1
        }
        round += 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunClassArrayStress(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Box {
    items: Array<i32>

    def init(self) {
        self.items = new Array<i32>(2)
    }
}

def main() -> i32 {
    var round: i32 = 0
    while round < 30 {
        var box: Box = Box()
        var i: i32 = 0
        while i < 500 {
            box.items.push(i * 3 + 1)
            i += 1
        }
        if box.items.len() != 500 {
            return 3000 + round
        }
        var j: i32 = 0
        while j < 500 {
            if box.items.get(usize(j)) != j * 3 + 1 {
                return 4000 + round
            }
            box.items.set(usize(j), j * 5 + 2)
            j += 1
        }
        j = 0
        while j < 500 {
            if box.items.get(usize(j)) != j * 5 + 2 {
                return 5000 + round
            }
            j += 1
        }
        var k: i32 = 0
        while k < 100 {
            match box.items.pop() {
                Some(value) => {
                    if value != (499 - k) * 5 + 2 {
                        return 6000 + round
                    }
                }
                None => {
                    return 7000 + round
                }
            }
            k += 1
        }
        if box.items.len() != 400 {
            return 8000 + round
        }
        round += 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunSelectClassLocalRoot(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Holder {
    value: i32

    def init(self, value: i32) {
        self.value = value
    }
}

async def pick(ch: chan<i32>) -> i32 {
    var holder: Holder = Holder(11)
    var i: i32 = 0
    while i < 100 {
        var tmp: Holder = Holder(i)
        if tmp.value < 0 {
            return 4
        }
        i += 1
    }
    select {
        item = ch.recv() => {
            match item {
                Some(v) => {
                    if v < 0 {
                        return 3
                    }
                }
                None => {
                    return 3
                }
            }
        }
        default => {
        }
    }
    return holder.value
}

async def main() -> i32 {
    var ch: chan<i32> = new chan<i32>(1)
    var first: i32 = await pick(ch)
    if first != 11 {
        return 1
    }
    _ = await ch.send(3)
    var second: i32 = await pick(ch)
    if second != 11 {
        return 2
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunCpuSyncPrimitives(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "cpusync")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make module: %v", err)
	}
	writeFile(t, dir, "will.mod", "module example.com/cpusync\nwill 1\n"+
		"require std@v0.1.0\nreplace std@v0.1.0 => "+filepath.ToSlash(filepath.Join(root, "std"))+"\n")
	src := `import "std/sync"

def main() -> i32 {
    var mu: sync.Mutex<i32> = new sync.Mutex<i32>(0)
    var g: sync.Guard<i32> = mu.lock_blocking()
    g.value = 7
    var taken: Option<sync.Guard<i32>> = mu.try_lock()
    match taken {
        Some(_) => {
            return 1
        }
        None => {}
    }
    var total: i32 = g.value
    var sem: sync.Semaphore = new sync.Semaphore(1)
    match sem.acquire_blocking() {
        Ok(p) => {
            p.release()
        }
        Err(_) => {
            return 2
        }
    }
    var count: sync.Count = new sync.Count(1)
    count.add(-1)
    match count.wait_blocking() {
        Ok(_) => {}
        Err(_) => {
            return 3
        }
    }
    var atom: sync.Atomic<i32> = new sync.Atomic<i32>(1)
    _ = atom.add(2)
    atom.store(4)
    if atom.load() != 4 or atom.cas(4, 9) == False {
        return 4
    }
    var once: sync.Once = new sync.Once()
    if once.done() {
        return 5
    }
    var body: func() = func() {
    }
    once.do_blocking(body)
    once.do_blocking(body)
    if once.done() == False {
        return 6
    }
    var barrier: sync.Barrier = new sync.Barrier(1)
    match barrier.wait_blocking() {
        Ok(_) => {}
        Err(_) => {
            return 8
        }
    }
    if total != 7 {
        return 9
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

func TestRunCpuSyncAcrossJobs(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "parallelsync")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make module: %v", err)
	}
	writeFile(t, dir, "will.mod", "module example.com/parallelsync\nwill 1\n"+
		"require std@v0.1.0\nreplace std@v0.1.0 => "+filepath.ToSlash(filepath.Join(root, "std"))+"\n")
	src := `import "std/sync"

def worker(mu: sync.Mutex<i32>, count: i32) -> i32 {
    var i: i32 = 0
    while i < count {
        var g: sync.Guard<i32> = mu.lock_blocking()
        g.value = g.value + 1
        i = i + 1
    }
    return 0
}

async def main() -> i32 {
    var mu: sync.Mutex<i32> = new sync.Mutex<i32>(0)
    var a: Task<i32> = spawn worker(mu.share(), 1000)
    var b: Task<i32> = spawn worker(mu.share(), 1000)
    var c: Task<i32> = spawn worker(mu.share(), 1000)
    var d: Task<i32> = spawn worker(mu.share(), 1000)
    _ = await a!
    _ = await b!
    _ = await c!
    _ = await d!
    var final: sync.Guard<i32> = await mu.lock()
    if final.value != 4000 {
        return 1
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

// TestRunClassReferencesInterfaceClosureAndAsync checks the root graph around
// class references: a self-referential class field, an interface box, a
// closure capture, and a class allocation inside an async function.
func TestRunClassReferencesInterfaceClosureAndAsync(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `interface Value {
    def get(self) -> i32
}

class Node {
    value: i32
    next: Node

    def init(self, value: i32) {
        self.value = value
    }

    def get(self) -> i32 {
        return self.value
    }

    def link(self, other: Node) {
        self.next = other
    }
}

class Holder {
    node: Node
}

def capture(n: Node) -> i32 {
    var f: func() -> i32 = func() -> i32 {
        return n.get()
    }
    return f()
}

async def make(value: i32) -> i32 {
    var n: Node = Node(value)
    return n.get()
}

async def main() -> i32 {
    var first: Node = Node(4)
    var second: Node = Node(5)
    first.link(second)
    if first.next.get() != 5 {
        return 1
    }
    var holder: Holder = Holder(node=first)
    if holder.node.get() != 4 {
        return 2
    }
    var iface: Value = second
    if iface.get() != 5 {
        return 3
    }
    if capture(first) != 4 {
        return 4
    }
    if await make(9) != 9 {
        return 5
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

// TestRunClassAsyncDefaultRoots checks a class constructor whose field default
// allocates another class inside an async frame. Both half-built objects must
// stay in the state-machine root map across the default's safepoint.
func TestRunClassAsyncDefaultRoots(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Inner {
    value: i32
}

class Outer {
    inner: Inner = Inner(value=1)
    value: i32 = 2
}

async def make() -> i32 {
    var o: Outer = Outer()
    return o.inner.value + o.value
}

async def main() -> i32 {
    var total: i32 = 0
    for _ in Range(0, 64) {
        total += await make()
    }
    if total != 192 {
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
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

// TestRunAsyncStructFieldRoot checks the same shadow-root rule in a general
// async frame: the struct local lives in the byte frame, not a C alloca, so its
// class field needs an explicit pointer slot in the task map.
func TestRunAsyncStructFieldRoot(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Node {
    value: i32
}

struct Holder {
    node: Node
}

async def wait() -> i32 {
    return 1
}

async def main() -> i32 {
    var h: Holder = Holder(node=Node(value=77))
    _ = await wait()
    var i: i32 = 0
    while i < 5000 {
        var junk: Node = Node(value=i)
        _ = junk
        i += 1
    }
    if h.node.value != 77 {
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
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

// TestRunStructFieldRoot checks that a class reference inside an inline struct
// local is mirrored into the exact GC frame root. A full cycle must not collect
// the object the struct field still names.
func TestRunStructFieldRoot(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `class Node {
    value: i32
}

struct Holder {
    node: Node
}

def main() -> i32 {
    var h: Holder = Holder(node=Node(value=77))
    var i: i32 = 0
    while i < 2000 {
        var junk: Node = Node(value=i)
        _ = junk
        i += 1
    }
    return h.node.value
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 77 {
		t.Fatalf("code = %d, want 77, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

// TestRunEarlyRcRelease checks that releasing a copy-only rc handle at its
// last use leaves the retained copy alive.
func TestRunEarlyRcRelease(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `struct Counter {
    n: i32
}

def main() -> i32 {
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
// frame moves an owning capture; the source release must not free the value
// the caller's closure still reads.
func TestRunEscapingClosureOwnsItsCapture(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `def make() -> func() -> i32 {
    var text: str = "hello".clone()
    return func() -> i32 {
        return i32(text.len())
    }
}

def main() -> i32 {
    var f: func() -> i32 = make()
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

// TestRunTwoClosuresMoveSeparateOwners checks the clone-at-the-transfer rule:
// each closure gets its own string and each environment releases it once.
func TestRunTwoClosuresMoveSeparateOwners(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `def run() -> i32 {
    var text: str = "hello" + "!"
    var copy: str = text.clone()
    var f: func() -> usize = func() -> usize {
        return text.len()
    }
    var g: func() -> usize = func() -> usize {
        return copy.len()
    }
    return i32(f() + g())
}

def main() -> i32 {
    return run() - 12
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

// TestRunLoopClosureDropsInPackageMode checks a closure created in a loop of a
// package object: its drop thunk must be defined in the same object as the call.
func TestRunLoopClosureDropsInPackageMode(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	dir := filepath.Join(t.TempDir(), "loopcapture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make module: %v", err)
	}
	writeFile(t, dir, "will.mod", "module example.com/loopcapture\nwill 1\n")
	src := `def run() -> i32 {
    var text: str = "hello" + "!"
    var f: func() -> usize = func() -> usize {
        return 0
    }
    for _ in Range(0, 2) {
        var inner: str = text.clone()
        f = func() -> usize {
            return inner.len()
        }
    }
    return i32(f()) - 6
}

def main() -> i32 {
    return run()
}
`
	writeFile(t, dir, "main.will", src)
	code, out, errOut := runCLI("run", dir)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunNestedClosureMovesItsLocal checks that an inner closure created in an
// outer closure moves a local of that immediate body and releases it once.
func TestRunNestedClosureMovesItsLocal(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `def run() -> i32 {
    var make: func() -> func() -> usize = func() -> func() -> usize {
        var inner: str = "abc" + "d"
        return func() -> usize {
            return inner.len()
        }
    }
    var g: func() -> usize = make()
    return i32(g()) - 4
}

def main() -> i32 {
    return run()
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

// TestRunAsyncChildResultMovesOnce checks that a typed async child result is
// moved into its local and then released once; the frame drop must see a clear
// slot instead of the same string owner.
func TestRunAsyncChildResultMovesOnce(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "childresult")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make module: %v", err)
	}
	writeFile(t, dir, "will.mod", "module example.com/childresult\nwill 1\n"+
		"require std@v0.1.0\nreplace std@v0.1.0 => "+filepath.ToSlash(filepath.Join(root, "std"))+"\n")
	src := `import "std/time"

async def text() -> str {
    _ = await time.sleep(1ms)!
    return "hello".clone()
}

async def parent() -> i32 {
    var text: str = await text()
    return i32(text.len())
}

async def main() -> i32 {
    return await parent() - 5
}
`
	writeFile(t, dir, "main.will", src)
	code, out, errOut := runCLI("run", dir)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunTryChildResultMovesOnce checks a child Result crossing through ? and
// then into a local: the try slot and the child slot must not both release it.
func TestRunTryChildResultMovesOnce(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "childresulttry")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make module: %v", err)
	}
	writeFile(t, dir, "will.mod", "module example.com/childresulttry\nwill 1\n"+
		"require std@v0.1.0\nreplace std@v0.1.0 => "+filepath.ToSlash(filepath.Join(root, "std"))+"\n")
	src := `import "std/time"

async def text() -> Result<str, Error> {
    _ = await time.sleep(1ms)!
    return Ok("hello".clone())
}

async def parent() -> Result<i32, Error> {
    var text: str = (await text())?
    return Ok(i32(text.len()))
}

async def main() -> i32 {
    return match await parent() {
        Ok(n) => n - 5
        Err(_) => 1
    }
}
`
	writeFile(t, dir, "main.will", src)
	code, out, errOut := runCLI("run", dir)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunTaskErrorOwnsItsMessage checks that a TaskError returned out of its
// region copies the task-owned panic message instead of borrowing it.
func TestRunTaskErrorOwnsItsMessage(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `def fail(msg: str) -> i32 {
    panic(msg)
    return 0
}

async def capture() -> Result<i32, TaskError> {
    var result: Result<i32, TaskError> = Ok(0)
    region {
        var task: Task<i32> = spawn fail("boom".clone())
        result = await task
    }
    return result
}

async def main() -> i32 {
    match await capture() {
        Ok(_) => {
            return 1
        }
        Err(e) => {
            var text: str = e.message()
            if text == "boom" {
                return 0
            }
            return 2
        }
    }
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if strings.Contains(errOut, "free rejected") {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
	}
}

// TestRunTryMovesThePayload checks that ? copies the payload out, zeros the
// source Option, and lets the caller release the payload once.
func TestRunTryMovesThePayload(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `def take() -> Option<str> {
    var o: Option<str> = Some("abc".clone())
    var s: str = o?
    return Some(s)
}

def main() -> i32 {
    return match take() {
        Some(s) => i32(s.len())
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
	src := `def take() -> i32 {
    var o: Option<str> = Some("abcd".clone())
    var s: str = o!
    return i32(s.len())
}

def main() -> i32 {
    return take()
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

def main() -> i32 {
    var b: Box = Box { items: new Array<i32>(4) }
    b.items.push(7)
    var items: Array<i32> = b.items
    items.push(8)
    return i32(items.len())
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
	src := `def main() -> i32 {
    var v: interface {} = 7
    return match v.as<i32>() {
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

def (p: ref P) message() -> str {
    return p.s.clone()
}

def (p: ref P) status() -> i32 {
    return 1
}

struct H {
    e: Error
}

def main() -> i32 {
    var e: Error = P { s: "hello" }
    var h: H = H { e: e }
    var xs: Array<Error> = new Array<Error>(2)
    xs.push(e)
    return i32(h.e.message().len()) + i32(xs.get(0).message().len())
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
    def area() -> i32
}

interface Labeled {
    def label() -> str
}

struct Square {
    n: i32
}

def (s: ref Square) area() -> i32 {
    return s.n
}

def (s: ref Square) label() -> str {
    return "square"
}

def main() -> i32 {
    var s: Shape = Square { n: 3 }
    var l: Option<Labeled> = s.as<Labeled>()
    return match l {
        Some(x) => i32(x.label().len())
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
	src := `def main() -> i32 {
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
	src := `async def main() -> i32 {
    var ch: chan<str> = new chan<str>(2)
    var ch2: chan<str> = ch
    var sent: Result<(), Error> = await ch2.send("hello")
    match sent {
        Ok(_) => {}
        Err(_) => {
            return 1
        }
    }
    var got: Option<str> = await ch.recv()
    var n: i32 = match got {
        Some(v) => i32(v.len())
        None => 0
    }
    ch.close()
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

def main() -> i32 {
    var a: str = "ab"
    var xs: [2]str = [2]str { a, "cde" }
    var ps: [2]S = [2]S { S { s: "xy" }, S { s: "z" } }
    return i32(xs[0].len() + xs[1].len() + ps[0].s.len() + ps[1].s.len())
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

def (p: ref P) message() -> str {
    return p.s.clone()
}

def (p: ref P) status() -> i32 {
    return 1
}

def main() -> i32 {
    var e: Error = P { s: "hello" }
    var f: Error = e.clone()
    var n: i32 = i32(f.message().len())
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
	src := `def count(xs: Array<str>) -> i32 {
    return i32(xs.len())
}

async def main() -> i32 {
    var xs: Array<str> = new Array<str>(2)
    xs.push("a")
    xs.push("b")
    var t: Task<i32> = spawn count(xs)
    return await t!
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

def main() -> i32 {
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

def field_return() -> str {
    var s: S = S { x: "abc", y: "de" }
    return s.x
}

def index_return() -> str {
    var xs: Array<str> = new Array<str>(2)
    xs.push("wxyz")
    return xs[0]
}

def moves() -> i32 {
    var s: S = S { x: "abc", y: "de" }
    var t: T = T { x: s.x }
    var s2: S = S { x: "fg", y: "hij" }
    var o: Option<str> = Some(s2.y)
    var n: i32 = i32(t.x.len())
    return n + match o {
        Some(v) => i32(v.len())
        None => 0
    }
}

def assign_move() -> i32 {
    var a: S = S { x: "abc", y: "de" }
    var b: S
    b.x = a.x
    b.y = "z"
    return i32(b.x.len() + b.y.len())
}

def main() -> i32 {
    return i32(field_return().len() + index_return().len()) + moves() + assign_move()
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
    text(str)
}

struct H {
    e: E
}

struct F {
    xs: [2]str
}

def text_len(e: E) -> i32 {
    return match e {
        text(s) => i32(s.len())
        Empty => 0
    }
}

def main() -> i32 {
    var h: H = H { e: E.text("hello") }
    var hc: H = h.clone()

    var f: F
    f.xs[0] = "ab"
    f.xs[1] = "cde"
    var fc: F = f.clone()

    var e2: E = E.text("xy")
    var e3: E = e2.clone()
    var xs: [2]str
    xs[0] = "z"
    var ys: [2]str = xs.clone()

    return text_len(hc.e) + text_len(e3) + i32(fc.xs[0].len() + fc.xs[1].len() + ys[0].len())
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

// TestRunBranchMoveReleasesThePathThatStillOwnsTheValue checks the runtime
// zeroing of a moved source: the moved path releases nothing, the other path
// releases the value exactly once.
func TestRunBranchMoveReleasesThePathThatStillOwnsTheValue(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `def take(flag: bool) -> i32 {
    var a: Array<i32> = new Array<i32>(4)
    a.push(7)
    if flag {
        var b: Array<i32> = a
        b.push(8)
        return i32(b.len())
    }
    return i32(a.len())
}

def main() -> i32 {
    return take(True) + take(False)
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

def main() -> i32 {
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
    text(str)
    Number(i32)
}

def main() -> i32 {
    var e: E = E.text("hello".clone())
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
func TestRunVariantEnumRoot(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	t.Setenv("WILL_GC_MEMLIMIT", "1")
	src := `enum E {
    text(str)
    Number(i32)
}

class Box {
    value: E
    label: str

    def init(self, value: E) {
        self.value = value.clone()
        self.label = "box"
    }
}

def main() -> i32 {
    var round: i32 = 0
    while round < 120 {
        var local: E = E.text("local=" + str(round))
        var box: Box = Box(E.text("class=" + str(round)))
        var i: i32 = 0
        while i < 100 {
            var tmp: str = "x" + str(i)
            if tmp.len() < 1usize {
                return 2000 + round
            }
            i += 1
        }
        match local {
            text(s) => {
                if s.len() < 7usize {
                    return 1000 + round
                }
            }
            Number(n) => {
                _ = n
                return 3000 + round
            }
        }
        if box.label.len() != 3usize {
            return 4500 + round
        }
        match box.value {
            text(s) => {
                if s.len() < 7usize {
                    return 4000 + round
                }
            }
            Number(n) => {
                _ = n
                return 5000 + round
            }
        }
        round += 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}

func TestRunMovedArrayIsReleasedOnce(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `def main() -> i32 {
    var first: Array<i32> = new Array<i32>(4)
    first.push(7)
    var second: Array<i32> = first
    second.push(8)
    return i32(second.len())
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
	src := `def main() -> i32 {
    var text: str = "abc".clone()
    var xs: Array<str> = new Array<str>(0)
    xs.push(text)
    return i32(xs.get(0).len())
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
	src := `def main() -> i32 {
    var xs: Array<i32> = new Array<i32>(4)
    xs.push(7)
    var n: usize = xs.len()
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

async def nap() -> Result<(), Error> {
    return await time.sleep(50ms)
}

async def wait() -> Option<i32> {
    match await time.sleep(50ms) {
        Ok(_) => {
            return Some(1)
        }
        Err(_) => {
            return None
        }
    }
}

async def main() -> i32 {
    var saw: bool = False
    deadline 1ms {
        var result: Result<(), Error> = await nap()
        match result {
            Err(e) => {
                if e.status() != 9 {
                    return 1
                }
                saw = True
            }
            Ok(_) => {
                return 2
            }
        }
    }
    if not saw {
        return 3
    }
    var none: bool = False
    deadline 1ms {
        var item: Option<i32> = await wait()
        match item {
            None => {
                none = True
            }
            Some(_) => {
                return 4
            }
        }
    }
    if not none {
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

// TestRunAsyncSpawnMovesOwnedArgument checks that a spawned async frame keeps
// working after the caller's owned argument has been cleared by the move.
func TestRunAsyncSpawnMovesOwnedArgument(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "asyncspawnarg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make module: %v", err)
	}
	writeFile(t, dir, "will.mod", "module example.com/asyncspawnarg\nwill 1\n"+
		"require std@v0.1.0\nreplace std@v0.1.0 => "+filepath.ToSlash(filepath.Join(root, "std"))+"\n")
	src := `import "std/time"

async def worker(text: str) -> i32 {
    _ = await time.sleep(1ms)!
    return i32(text.len())
}

async def main() -> i32 {
    var result: i32 = 0
    region {
        var text: str = "hello".clone()
        var task: Task<i32> = spawn worker(text)
        result = await task!
    }
    return result
}
`
	writeFile(t, dir, "main.will", src)
	code, out, errOut := runCLI("run", dir)
	if code != 5 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no allocator rejection", errOut)
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

async def worker() -> i32 {
    var text: str = "hello"
    var items: Array<i32> = new Array<i32>(2)
    _ = await time.sleep(1ms)!
    panic("stop")
    return i32(text.len()) + i32(items.len())
}

async def main() -> i32 {
    region {
        var task: Task<i32> = spawn worker()
        match await task {
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
	src := `def work() -> i64 {
    return 1234567890123i64
}

async def main() -> i32 {
    region {
        var t: Task<i64> = spawn work()
        var v: i64 = await t!
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
    def area() -> i32
}

interface Labeled {
    def label() -> str
}

struct Square {
    n: i32
}

def (s: ref Square) area() -> i32 {
    return s.n
}

def (s: ref Square) label() -> str {
    return "square"
}

def main() -> i32 {
    var shape: Shape = Square { n: 12 }
    if not shape.is<Square>() {
        return 1
    }
    var square: Option<Square> = shape.as<Square>()
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
    var labeled: Option<Labeled> = shape.as<Labeled>()
    match labeled {
        Some(value) => {
            if value.label() != "square" {
                return 4
            }
        }
        None => {
            return 5
        }
    }
    var empty: Shape
    if empty.is<Square>() {
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

def pick() -> Choice {
    return B
}

def main() -> i32 {
    if pick() != B {
        return 1
    }
    if pick() == C {
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
	src := `def number(value: interface {}) -> i32 {
    var got: Option<i32> = value.as<i32>()
    match got {
        Some(number) => {
            return number
        }
        None => {
            return -1
        }
    }
}

def main() -> i32 {
    var boxed: interface {} = 42
    if not boxed.is<i32>() {
        return 1
    }
    if number(boxed) != 42 {
        return 2
    }
    if boxed.is<str>() {
        return 3
    }
    var text: interface {} = "will"
    if not text.is<str>() {
        return 4
    }
    var empty: interface {}
    if empty.is<i32>() {
        return 5
    }
    var same: Option<interface {}> = boxed.as<interface {}>()
    match same {
        Some(value) => {
            if not value.is<i32>() {
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
	src := `def inc(value: mut i32) -> i32 {
    value += 1
    return value
}

async def add(value: mut i32) -> i32 {
    value += 2
    return value
}

async def main() -> i32 {
    var first: i32 = 1
    var inc: i32 = inc(first)
    var second: i32 = 4
    var add: i32 = await add(second)
    print(first, " ", inc, " ", second, " ", add, "\n")
    if first != 2 or inc != 2 or second != 6 or add != 6 {
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

async def (c: mut Counter) add(value: i32) -> i32 {
    c.n += value
    return c.n
}

async def grow(values: mut Array<i32>) -> i32 {
    values.push(7)
    return i32(values.len())
}

async def main() -> i32 {
    var counter: Counter = Counter { n: 1 }
    var added: i32 = await counter.add(4)
    var values: Array<i32> = new Array<i32>(0)
    var grown: i32 = await grow(values)
    print(counter.n, " ", values.len(), "\n")
    if counter.n != 5 or added != 5 or grown != 1 or values.len() != 1 {
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
	src := `async def produce(ch: chan<i32>, n: i32) {
    for i in Range(0, n) {
        _ = await ch.send(i)
    }
}

async def consume(ch: chan<i32>) -> i64 {
    var sum: i64 = 0
    while True {
        var item: Option<i32> = await ch.recv()
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

async def main() -> i32 {
    var ch: chan<i32> = new chan<i32>(64)
    var total: i64 = 0
    region {
        var p1: Task<()> = spawn produce(ch, 20000)
        var p2: Task<()> = spawn produce(ch, 20000)
        var c1: Task<i64> = spawn consume(ch)
        var c2: Task<i64> = spawn consume(ch)
        _ = await p1!
        _ = await p2!
        ch.close()
        var c1v: i64 = await c1!
        var c2v: i64 = await c2!
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

// TestRunLoopAllocaDoesNotGrowStack checks that array temporaries created in a
// loop stay in the entry block. An alloca executed per iteration overflows the
// default thread stack at 65536 iterations; this loop is above that bound.
func TestRunLoopAllocaDoesNotGrowStack(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	src := `def main() -> i32 {
    var i: i32 = 0
    while i < 70000 {
        var a: Array<i32> = new Array<i32>(4)
        a.push(i)
        var c: Array<i32> = a.clone()
        var n: usize = c.len()
        _ = n
        i += 1
    }
    return 0
}
`
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, errOut := runCLI("run", path)
	if code != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want no diagnostics", errOut)
	}
}
