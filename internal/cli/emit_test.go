package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitWillc(t *testing.T) {
	path := writeFile(t, t.TempDir(), "add.will", goodSource)
	code, out, errOut := runCLI("emit-willc", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if !strings.HasPrefix(out, "#1 module ") {
		t.Fatalf("output does not start with the module record:\n%s", out)
	}
	if !strings.Contains(out, `text="Add"`) {
		t.Fatalf("output lacks the function record:\n%s", out)
	}
	if !strings.HasSuffix(out, "end\n") {
		t.Fatalf("output does not end with the end record:\n%s", out)
	}
}

func TestEmitWillcOut(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "add.will", goodSource)
	out := filepath.Join(dir, "add.willc")
	if code, _, errOut := runCLI("emit-willc", "--out", out, path); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if !strings.HasPrefix(string(data), "#1 module ") {
		t.Fatalf("out file does not start with the module record:\n%s", data)
	}
}

func TestEmitWillcRefusesBadProgram(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.will", badSource)
	code, out, _ := runCLI("emit-willc", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "T0001") {
		t.Fatalf("stdout lacks the diagnostic:\n%s", out)
	}
	if strings.Contains(out, "#1 module") {
		t.Fatalf("a bad program still produced willc:\n%s", out)
	}
}

func TestEmitWillcUsage(t *testing.T) {
	if code, _, _ := runCLI("emit-willc"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
