package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	code, out, _ := runCLI("version")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	for _, want := range []string{"will 0.0.0-dev", "target ", "opt -O0", "binary "} {
		if !strings.Contains(out, want) {
			t.Errorf("version output lacks %q:\n%s", want, out)
		}
	}
}

func TestNewModule(t *testing.T) {
	t.Chdir(t.TempDir())
	arg := "example.com/app"
	code, _, errOut := runCLI("new", arg)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	root := filepath.FromSlash(arg)
	mod, err := os.ReadFile(filepath.Join(root, "will.mod"))
	if err != nil {
		t.Fatalf("read will.mod: %v", err)
	}
	if string(mod) != "module "+arg+"\nwill 1\n" {
		t.Fatalf("will.mod = %q", mod)
	}
	main, err := os.ReadFile(filepath.Join(root, "src", "main.will"))
	if err != nil {
		t.Fatalf("read main.will: %v", err)
	}
	if string(main) != mainSource {
		t.Fatalf("main.will = %q", main)
	}
	// The generated module must parse and check.
	if code, out, _ := runCLI("check", root); code != 0 || out != "" {
		t.Fatalf("new module does not check: code = %d, stdout = %q", code, out)
	}
}

func TestNewRefusesExistingModule(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, _ := runCLI("new", "example.com/app"); code != 0 {
		t.Fatalf("first new code = %d", code)
	}
	if code, _, errOut := runCLI("new", "example.com/app"); code != 1 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("second new: code = %d, stderr = %q", code, errOut)
	}
}

func TestNewUsage(t *testing.T) {
	if code, _, _ := runCLI("new"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
