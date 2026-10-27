package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestFileModeFindsTheModuleRoot checks that a file in a subdirectory of its
// module sees the module's packages. Looking only at the file's directory lost
// the module, so every import became an unknown path and will check disagreed
// with will build.
func TestFileModeFindsTheModuleRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	writeTree(t, root, map[string]string{
		"will.mod":      "module example.com/app\nwill 1\n",
		"src/main.will": "import \"example.com/app/lib\"\n\ndef main() -> i32 {\n    return lib.double(2)\n}\n",
		"lib/lib.will":  "def double(n: i32) -> i32 {\n    return n + n\n}\n",
	})
	file := filepath.Join(root, "src", "main.will")

	if code, out, errOut := runCLI("check", file); code != 0 {
		t.Fatalf("check: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	code, out, errOut := runCLI("emit-llvm", file)
	if code != 0 {
		t.Fatalf("emit: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if !strings.Contains(out, "example.com/app/lib.double") {
		t.Fatalf("the import did not resolve:\n%s", out)
	}
}

// TestFileModeAgreesWithBuildOnErrors checks that the two commands report the
// same diagnostic for one file.
func TestFileModeAgreesWithBuildOnErrors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	writeTree(t, root, map[string]string{
		"will.mod":      "module example.com/app\nwill 1\n",
		"src/main.will": "def main() -> i32 {\n    var n: i32 = \"text\"\n    return n\n}\n",
	})
	file := filepath.Join(root, "src", "main.will")
	checkCode, checkOut, _ := runCLI("check", file)
	buildCode, buildOut, buildErr := runCLI("emit-llvm", file)
	if checkCode != 1 || buildCode != 1 {
		t.Fatalf("codes = %d and %d, want 1 and 1", checkCode, buildCode)
	}
	if buildOut != "" {
		t.Fatalf("emit-llvm wrote diagnostics to stdout: %q", buildOut)
	}
	if checkOut != buildErr {
		t.Fatalf("check said %q, emit-llvm said %q", checkOut, buildErr)
	}
}
