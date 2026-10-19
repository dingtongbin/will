package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindAssembly(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.c", []byte("int main(void) { return 0; }\n"))
	write(t, root, filepath.Join("runtime", "core", "task.S"), []byte("nop\n"))
	write(t, root, filepath.Join("build", "old.s"), []byte("nop\n"))
	found, err := findAssembly(root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 1 || found[0] != "runtime/core/task.S" {
		t.Fatalf("found = %v, want runtime/core/task.S", found)
	}
}

func write(t *testing.T, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
