package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const messySource = "def add( ) {\n    var x : i32=1+2\n}\n"

const canonicalSource = "def add() {\n    var x: i32 = 1 + 2\n}\n"

func TestFmtWritesCanonicalText(t *testing.T) {
	path := writeFile(t, t.TempDir(), "messy.will", messySource)
	if code, _, errOut := runCLI("fmt", path); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != canonicalSource {
		t.Fatalf("formatted = %q, want %q", data, canonicalSource)
	}
	if code, _, _ := runCLI("fmt", "--check", path); code != 0 {
		t.Fatalf("--check on canonical text = %d, want 0", code)
	}
}

func TestFmtCheckReportsUnformatted(t *testing.T) {
	path := writeFile(t, t.TempDir(), "messy.will", messySource)
	code, out, _ := runCLI("fmt", "--check", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "is not formatted") {
		t.Fatalf("stdout = %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != messySource {
		t.Fatalf("--check rewrote the file: %q", data)
	}
}

func TestFmtUsage(t *testing.T) {
	if code, _, _ := runCLI("fmt"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestFmtKeepsDirectoryUntouched(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "x.will", canonicalSource)
	if code, _, _ := runCLI("fmt", path); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	info, err := os.Stat(filepath.Join(dir, "x.will"))
	if err != nil || info.IsDir() {
		t.Fatalf("stat: %v", err)
	}
}
