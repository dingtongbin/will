package pkg

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
)

// writeStdTree writes a standard library module with net/http and returns the
// root directory. The module is an ordinary local replace target.
func writeStdTree(t *testing.T) string {
	t.Helper()
	std := filepath.Join(t.TempDir(), "std")
	write(t, filepath.Join(std, "will.mod"), "module std\nwill 1\n")
	write(t, filepath.Join(std, "net", "http", "http.will"), "func Get() -> i32 {\n    return 1\n}\n")
	return std
}

// stdApp writes a module that requires std and replaces the requirement with
// the given local tree.
func stdApp(t *testing.T, std string) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(app, "will.mod"),
		"module example.com/app\nwill 1\nrequire std@v0.1.0\nreplace std@v0.1.0 => "+filepath.ToSlash(std)+"\n")
	write(t, filepath.Join(app, "main.will"),
		"import \"std/net/http\"\n\nfunc F() -> i32 {\n    return http.Get()\n}\n")
	return app
}

// TestStdResolvesThroughReplace checks that an explicit require plus a local
// replace supplies the standard packages.
func TestStdResolvesThroughReplace(t *testing.T) {
	std := writeStdTree(t)
	app := stdApp(t, std)

	b := Load(app)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	if len(b.Diags) != 0 {
		t.Fatalf("load diagnostics:\n%s", diag.FormatAll(b.Diags))
	}
	if ds := b.CheckAll(); len(ds) != 0 {
		t.Fatalf("check diagnostics:\n%s", diag.FormatAll(ds))
	}
	found := false
	for _, p := range b.Packages {
		if p.Path == "std/net/http" {
			found = true
			if p.Name != "http" {
				t.Errorf("package name = %q", p.Name)
			}
		}
	}
	if !found {
		t.Fatalf("std/net/http is missing from the graph")
	}
}

// TestStdMissingSubpackage checks that a replaced root without the imported
// package is an ordinary unknown import path, not a toolchain fault.
func TestStdMissingSubpackage(t *testing.T) {
	std := filepath.Join(t.TempDir(), "std")
	write(t, filepath.Join(std, "will.mod"), "module std\nwill 1\n")
	write(t, filepath.Join(std, "net", "other", "other.will"), "func Get() -> i32 {\n    return 1\n}\n")
	app := stdApp(t, std)

	b := Load(app)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	d := findCode(t, b.CheckAll(), diag.CodeUnknownImportPath)
	if d.Message != "unknown import path std/net/http" {
		t.Errorf("message = %q", d.Message)
	}
}

// TestStdImportWithoutRequire checks that importing std without a requirement
// is an unknown import path. The toolchain has no implicit root.
func TestStdImportWithoutRequire(t *testing.T) {
	app := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(app, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(app, "main.will"),
		"import \"std/net/http\"\n\nfunc F() -> i32 {\n    return http.Get()\n}\n")

	b := Load(app)
	if len(b.Diags) != 0 {
		t.Fatalf("load diagnostics:\n%s", diag.FormatAll(b.Diags))
	}
	d := findCode(t, b.CheckAll(), diag.CodeUnknownImportPath)
	if !strings.Contains(d.Message, "std/net/http") {
		t.Errorf("message = %q", d.Message)
	}
}
