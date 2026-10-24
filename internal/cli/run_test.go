package cli

import (
	"strings"
	"testing"
)

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
