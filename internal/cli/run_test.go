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

func main() -> i32 {
    region {
        var t: Task<i64> = spawn Work()
        var v: i64 = t.Join()!
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
