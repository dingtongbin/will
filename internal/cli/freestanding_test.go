package cli

import (
	"strings"
	"testing"
)

const freestandingSource = `def f() {
    var xs: Array<i64> = new Array<i64>(0)
    _ = xs
}
`

func TestCheckFreestandingReportsRuntimeUse(t *testing.T) {
	path := writeFile(t, t.TempDir(), "a.will", freestandingSource)
	// Without the flag the program is fine.
	if code, out, _ := runCLI("check", path); code != 0 {
		t.Fatalf("check without the flag: code = %d, stdout = %q", code, out)
	}
	code, out, _ := runCLI("check", "--freestanding", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0007 the runtime is not available with --freestanding") ||
		!strings.Contains(out, "    help: remove this call, or drop --freestanding") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestCheckFreestandingReportsPackage(t *testing.T) {
	src := "import \"std/net\"\n\ndef f() {\n}\n"
	path := writeFile(t, t.TempDir(), "b.will", src)
	code, out, _ := runCLI("check", "--freestanding", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0007 package std/net is not available with --freestanding") ||
		!strings.Contains(out, "    help: only std/math is allowed; drop --freestanding to use it") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestCheckFreestandingAcceptsPureCode(t *testing.T) {
	path := writeFile(t, t.TempDir(), "c.will", goodSource)
	if code, out, _ := runCLI("check", "--freestanding", path); code != 0 {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
}

func TestBuildFreestandingReportsTheSameDiagnostics(t *testing.T) {
	if hasClang() {
		t.Skip("clang is installed; the build would go further than diagnostics")
	}
	path := writeFile(t, t.TempDir(), "main.will", "def main() {\n    panic(\"stop\")\n}\n")
	code, out, _ := runCLI("build", "--freestanding", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0007 the runtime is not available with --freestanding") {
		t.Fatalf("stdout = %q", out)
	}
}
