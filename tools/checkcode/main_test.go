package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

func rules(findings []Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Rule)
	}
	return out
}

func has(findings []Finding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func TestCleanTree(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.go", []byte("// Package main is a program.\npackage main\n"))
	write(t, root, "a.will", []byte("def main() {\n}\n"))
	findings, err := Check(root)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %v", findings)
	}
}

func TestRules(t *testing.T) {
	root := t.TempDir()
	write(t, root, "bom.go", append([]byte{0xEF, 0xBB, 0xBF}, []byte("package main\n")...))
	write(t, root, "crlf.go", []byte("package main\r\n"))
	write(t, root, "chinese.go", []byte("// \u4e2d\u6587\npackage main\n"))
	write(t, root, "noise.go", []byte("// TODO fix this\npackage main\n"))
	write(t, root, "doc.txt", []byte("\xff\xfe binary\n"))
	findings, err := Check(root)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, rule := range []string{"bom", "crlf", "ascii", "noise"} {
		if !has(findings, rule) {
			t.Errorf("no %s finding in %v", rule, findings)
		}
	}
	if len(findings) != 4 {
		t.Errorf("findings = %d, want 4: %v", len(findings), findings)
	}
}

func TestDescIsSkipped(t *testing.T) {
	root := t.TempDir()
	write(t, root, filepath.Join("desc", "design.md"), []byte("# \u8bbe\u8ba1\n"))
	findings, err := Check(root)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %v", findings)
	}
}

func TestLineNumbers(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", []byte("package main\n\n// FIXME later\n"))
	findings, err := Check(root)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(findings) != 1 || findings[0].Line != 3 {
		t.Fatalf("findings = %v", findings)
	}
	if findings[0].Path != "a.go" {
		t.Errorf("path = %q, want a.go", findings[0].Path)
	}
}
