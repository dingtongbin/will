package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const entrySource = `import c "stdio.h" {
    fast putchar
}

func main() -> i32 {
    return c.putchar(65)
}
`

func hasClang() bool {
	_, err := exec.LookPath("clang")
	return err == nil
}

// buildWarningSource has one warning and an entry function, so it can be both
// checked and built.
const buildWarningSource = `func F() {
    var i32: i32 = 0
    _ = i32
}

func main() {
}
`

// TestBuildReportsTheSameDiagnostics checks the rule that will check and will
// build report the same diagnostics.
func TestNewModuleLowers(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, errOut := runCLI("new", "example.com/app"); code != 0 {
		t.Fatalf("will new: code = %d, stderr = %q", code, errOut)
	}
	code, out, errOut := runCLI("emit-llvm", filepath.FromSlash("example.com/app"))
	if code != 0 {
		t.Fatalf("emit-llvm: code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, `define void @"example.com/app/src.will$main"()`) || !strings.Contains(out, "define i32 @main()") {
		t.Fatalf("the generated module does not lower:\n%s", out)
	}
}

func TestEmitLLVMCrossPackage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "crosspkg")
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, root, "will.mod", "module example.com/crosspkg\nwill 1\n")
	writeFile(t, root, "main.will", "import \"example.com/crosspkg/lib\"\n\nfunc main() -> i32 {\n    return lib.Util()\n}\n")
	writeFile(t, filepath.Join(root, "lib"), "lib.will", "func Util() -> i32 {\n    return 7\n}\n")

	code, out, errOut := runCLI("emit-llvm", root)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	for _, want := range []string{
		"define i32 @main() {",
		`call i32 @"example.com/crosspkg/lib.Util"()`,
		`define i32 @"example.com/crosspkg/lib.Util"() {`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("IR lacks %q:\n%s", want, out)
		}
	}
}

func TestEmitDecisions(t *testing.T) {
	path := writeFile(t, t.TempDir(), "a.will", "struct Point {\n    x: i64\n    y: i64\n}\n")
	code, out, errOut := runCLI("emit-decisions", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	want := "layout type=\"Point\" size=16 align=8\n" +
		"field type=\"Point\" name=\"x\" offset=0 size=8\n" +
		"field type=\"Point\" name=\"y\" offset=8 size=8\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestEmitDecisionsUsage(t *testing.T) {
	if code, _, _ := runCLI("emit-decisions"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestEmitDecisionsNestedAndEnum(t *testing.T) {
	src := "struct Inner {\n    v: i32\n}\n\nstruct Outer {\n    tag: u8\n    inner: Inner\n}\n\nenum Value {\n    None\n    Number(i64)\n}\n"
	path := writeFile(t, t.TempDir(), "a.will", src)
	code, out, errOut := runCLI("emit-decisions", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	want := "layout type=\"Inner\" size=4 align=4\n" +
		"field type=\"Inner\" name=\"v\" offset=0 size=4\n" +
		"layout type=\"Outer\" size=8 align=4\n" +
		"field type=\"Outer\" name=\"tag\" offset=0 size=1\n" +
		"field type=\"Outer\" name=\"inner\" offset=4 size=4\n" +
		"layout type=\"Value\" size=16 align=8\n" +
		"field type=\"Value\" name=\"tag\" offset=0 size=4\n" +
		"field type=\"Value\" name=\"None\" offset=8 size=0\n" +
		"field type=\"Value\" name=\"Number\" offset=8 size=8\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestEmitDecisionsObligations(t *testing.T) {
	// A container loop proves its index check; a Range loop is its own index
	// sequence and records nothing. The nested loop inside the if must be
	// found too.
	src := "func F(xs: [2]i32) -> i32 {\n" +
		"    var n: i32 = 0\n" +
		"    for x in xs {\n        n += x\n    }\n" +
		"    for i in Range(0, 3) {\n        n += i\n    }\n" +
		"    if n > 0 {\n        for y in xs {\n            n += y\n        }\n    }\n" +
		"    return n\n}\n"
	path := writeFile(t, t.TempDir(), "a.will", src)
	code, out, errOut := runCLI("emit-decisions", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if got := strings.Count(out, `check="index" verdict="proved" by="range"`); got != 2 {
		t.Fatalf("obligations = %d, want 2:\n%s", got, out)
	}
	for _, want := range []string{"a.will:3:5\"", "a.will:10:9\""} {
		if !strings.Contains(out, want) {
			t.Errorf("the record lacks a position ending in %q:\n%s", want, out)
		}
	}
}

func TestEmitDecisionsRangeHasNoObligation(t *testing.T) {
	src := "func F() -> i32 {\n    var n: i32 = 0\n    for i in Range(0, 3) {\n        n += i\n    }\n    return n\n}\n"
	path := writeFile(t, t.TempDir(), "a.will", src)
	code, out, errOut := runCLI("emit-decisions", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if strings.Contains(out, "obligation") {
		t.Fatalf("a Range loop recorded an obligation:\n%s", out)
	}
}

func TestEmitLLVMUsage(t *testing.T) {
	if code, _, _ := runCLI("emit-llvm"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestEmitAsmNeedsClang(t *testing.T) {
	if hasClang() {
		t.Skip("clang is installed; the missing-tool path is not reachable")
	}
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, _ := runCLI("emit-asm", path)
	if code != 1 || !strings.Contains(out, "X0003 external tool clang is missing or too old") {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
}

func TestEmitAsmWithClang(t *testing.T) {
	if !hasClang() {
		t.Skip("clang is not installed")
	}
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, errOut := runCLI("emit-asm", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "putchar") {
		t.Fatalf("assembly does not mention putchar:\n%s", out)
	}
}

func TestBuildReportsTheSameDiagnostics(t *testing.T) {
	if hasClang() {
		t.Skip("clang is installed; the build would go further than diagnostics")
	}
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, root, "will.mod", "module example.com/app\nwill 1\n")
	writeFile(t, root, "app.will", buildWarningSource)

	_, checked, _ := runCLI("check", root)
	_, built, _ := runCLI("build", root)
	if !strings.Contains(checked, "N0003 this name shadows i32") {
		t.Fatalf("check did not report the warning: %q", checked)
	}
	if !strings.Contains(built, "N0003 this name shadows i32") {
		t.Fatalf("build did not report the same warning: %q", built)
	}

	_, quiet, _ := runCLI("build", "-Wno-shadow", root)
	if strings.Contains(quiet, "N0003") {
		t.Fatalf("build kept a switched-off warning: %q", quiet)
	}
}

func TestEmitLLVM(t *testing.T) {
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, errOut := runCLI("emit-llvm", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	for _, want := range []string{"declare i32 @putchar(i32)", "call i32 @putchar(i32 65)", "ret i32 %t1"} {
		if !strings.Contains(out, want) {
			t.Errorf("IR lacks %q:\n%s", want, out)
		}
	}
}

func TestBuildFlagsAreAccepted(t *testing.T) {
	if hasClang() {
		t.Skip("clang is installed; the missing-tool path is not reachable")
	}
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, _ := runCLI("build", "-O1", "-g", "--target", "x86_64-linux-gnu", "--lib", path)
	if code != 1 || !strings.Contains(out, "X0003") {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
}

func TestRunProgramArgsAreNotFlags(t *testing.T) {
	if hasClang() {
		t.Skip("clang is installed; the missing-tool path is not reachable")
	}
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, _, _ := runCLI("run", path, "--", "--verbose", "-O3")
	if code != 1 {
		t.Fatalf("code = %d, want 1 because clang is missing", code)
	}
}

func TestBuildUsesCache(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.will", entrySource)
	cacheDir := filepath.Join(dir, "cache")
	t.Setenv("WILL_CACHE", cacheDir)

	code1, out1, _ := runCLI("emit-llvm", path)
	if code1 != 0 || out1 == "" {
		t.Fatalf("first build: code = %d, stdout = %q", code1, out1)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v, err = %v", entries, err)
	}
	code2, out2, _ := runCLI("emit-llvm", path)
	if code2 != code1 || out2 != out1 {
		t.Fatalf("cached build differed: code = %d, stdout = %q", code2, out2)
	}
	// A changed source must miss.
	writeFile(t, dir, "main.will", strings.Replace(entrySource, "65", "66", 1))
	if _, out3, _ := runCLI("emit-llvm", path); out3 == out1 {
		t.Fatalf("changed source hit the old entry: %q", out3)
	}
}

func TestBuildMissingClang(t *testing.T) {
	if hasClang() {
		t.Skip("clang is installed; the missing-tool path is not reachable")
	}
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, _ := runCLI("build", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "X0003 external tool clang is missing or too old") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestBuildEntryNotFound(t *testing.T) {
	path := writeFile(t, t.TempDir(), "main.will", "func other() {\n}\n")
	code, out, _ := runCLI("build", "--entry", "main", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0006 no entry function main") ||
		!strings.Contains(out, "    help: declare it, or pass --entry <name>") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestUnsupportedTarget(t *testing.T) {
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, _ := runCLI("check", "--target", "sparc-linux-gnu", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0005 unsupported target sparc-linux-gnu") ||
		!strings.Contains(out, "    help: use one of the supported platforms: linux, mac, win, android, ios") {
		t.Fatalf("stdout = %q", out)
	}
	// A supported target goes through.
	if code, _, _ := runCLI("check", "--target", "aarch64-linux-gnu", path); code != 0 {
		t.Fatalf("a supported target was rejected: code = %d", code)
	}
}

func TestLoweringFailureIsG0001(t *testing.T) {
	// A local whose type is not lowered yet is a translation failure, not a
	// front-end error.
	src := "func main() -> i32 {\n    var s: str = \"x\"\n    _ = s\n    return 0\n}\n"
	path := writeFile(t, t.TempDir(), "main.will", src)
	code, out, _ := runCLI("emit-llvm", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "G0001 cannot translate node #") ||
		!strings.Contains(out, "    help: this is a compiler bug; report it with the willc file") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestBuildUsage(t *testing.T) {
	if code, _, _ := runCLI("build"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestRunMissingClang(t *testing.T) {
	if hasClang() {
		t.Skip("clang is installed")
	}
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, _ := runCLI("run", path)
	if code != 1 || !strings.Contains(out, "X0003") {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
}

var _ = errors.Is
