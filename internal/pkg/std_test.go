package pkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
)

// writeStdTree writes a bundled standard library root with net/http, and
// returns the root directory.
func writeStdTree(t *testing.T) string {
	t.Helper()
	std := filepath.Join(t.TempDir(), "std")
	write(t, filepath.Join(std, "will.mod"), "module std\nwill 1\n")
	write(t, filepath.Join(std, "net", "http", "http.will"), "func Get() -> i32 {\n    return 1\n}\n")
	return std
}

// stdApp writes a module that requires std@bundled and imports one std
// package.
func stdApp(t *testing.T) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(app, "will.mod"), "module example.com/app\nwill 1\nrequire std@bundled\n")
	write(t, filepath.Join(app, "main.will"),
		"import \"std/net/http\"\n\nfunc F() -> i32 {\n    return http.Get()\n}\n")
	return app
}

// TestBundledStdRootResolves checks that WILL_ROOT supplies the bundled root
// and that a std package under it resolves and checks.
func TestBundledStdRootResolves(t *testing.T) {
	std := writeStdTree(t)
	t.Setenv("WILL_ROOT", std)
	app := stdApp(t)

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

// TestBundledStdRootMissing checks that a missing root is a toolchain fault:
// Load returns the typed error and no import diagnostic.
func TestBundledStdRootMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "std")
	t.Setenv("WILL_ROOT", missing)
	app := stdApp(t)

	b := Load(app)
	if b.Err == nil {
		t.Fatal("load succeeded without the bundled standard library")
	}
	if !IsMissingBundledStd(b.Err) {
		t.Fatalf("error = %v, want MissingBundledStd", b.Err)
	}
	want := "the bundled standard library is missing: " + filepath.Clean(missing) + " (set WILL_ROOT)"
	if b.Err.Error() != want {
		t.Errorf("error = %q, want %q", b.Err.Error(), want)
	}
	for _, d := range b.Diags {
		if d.Code == diag.CodeUnknownImportPath {
			t.Fatalf("B0008 reported for a broken toolchain:\n%s", diag.FormatAll(b.Diags))
		}
	}
}

// TestBundledStdSubpackageMissing checks that a root that exists but does not
// hold the imported subpackage is an import path error, B0008.
func TestBundledStdSubpackageMissing(t *testing.T) {
	std := filepath.Join(t.TempDir(), "std")
	write(t, filepath.Join(std, "will.mod"), "module std\nwill 1\n")
	write(t, filepath.Join(std, "net", "other", "other.will"), "func Get() -> i32 {\n    return 1\n}\n")
	t.Setenv("WILL_ROOT", std)
	app := stdApp(t)

	b := Load(app)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	d := findCode(t, b.CheckAll(), diag.CodeUnknownImportPath)
	if d.Message != "unknown import path std/net/http" {
		t.Errorf("message = %q", d.Message)
	}
}

// TestStdRootBesideBinary pins the installed and development layout:
// <binary dir>/../std.
func TestStdRootBesideBinary(t *testing.T) {
	cases := []struct{ exe, want string }{
		{filepath.Join("inst", "bin", "will"), filepath.Join("inst", "std")},
		{filepath.Join("repo", "build", "will"), filepath.Join("repo", "std")},
	}
	for _, c := range cases {
		if got := stdRootBeside(c.exe); got != c.want {
			t.Errorf("stdRootBeside(%q) = %q, want %q", c.exe, got, c.want)
		}
	}
}

// TestStdRootIsNotReadWithoutStd checks that a module without std does not
// care that the bundled root is absent.
func TestStdRootIsNotReadWithoutStd(t *testing.T) {
	t.Setenv("WILL_ROOT", filepath.Join(t.TempDir(), "missing"))
	root := module(t, map[string]string{"main.will": "func F() {\n}\n"})
	b := Load(root)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	if len(b.Diags) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(b.Diags))
	}
}

// TestBundledStdRootFromWILLRoot checks that WILL_ROOT names the standard
// library root itself.
func TestBundledStdRootFromWILLRoot(t *testing.T) {
	std := writeStdTree(t)
	t.Setenv("WILL_ROOT", std)
	app := stdApp(t)

	b := Load(app)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	if ds := b.CheckAll(); len(ds) != 0 {
		t.Fatalf("check diagnostics:\n%s", diag.FormatAll(ds))
	}
}

// TestBundledStdRootMissingUnderWILLRoot checks that a WILL_ROOT without the
// standard library reports that root, not a child of it.
func TestBundledStdRootMissingUnderWILLRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	t.Setenv("WILL_ROOT", root)
	app := stdApp(t)

	b := Load(app)
	if !IsMissingBundledStd(b.Err) {
		t.Fatalf("error = %v, want MissingBundledStd", b.Err)
	}
	want := "the bundled standard library is missing: " + filepath.Clean(root) + " (set WILL_ROOT)"
	if b.Err.Error() != want {
		t.Errorf("error = %q, want %q", b.Err.Error(), want)
	}
}

// TestBundledStdRequiredWithoutImport checks that a bundled requirement alone
// needs the root: the dependency is part of the toolchain contract even before
// source imports it.
func TestBundledStdRequiredWithoutImport(t *testing.T) {
	t.Setenv("WILL_ROOT", filepath.Join(t.TempDir(), "std"))
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\nrequire std@bundled\n")
	write(t, filepath.Join(root, "main.will"), "func F() {\n}\n")
	b := Load(root)
	if !IsMissingBundledStd(b.Err) {
		t.Fatalf("error = %v, want MissingBundledStd", b.Err)
	}
}

// TestStdRootFromEnvironmentIsCleaned pins that the reported path is the
// cleaned root, not the raw value with separators the platform treats the same.
func TestStdRootFromEnvironmentIsCleaned(t *testing.T) {
	std := writeStdTree(t)
	raw := std + string(filepath.Separator) + "." + string(filepath.Separator)
	t.Setenv("WILL_ROOT", raw)
	got, ok := bundledStdRoot()
	if !ok {
		t.Fatal("bundledStdRoot did not find the tree")
	}
	if got != filepath.Clean(std) {
		t.Errorf("root = %q, want %q", got, filepath.Clean(std))
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !strings.EqualFold(filepath.Base(got), "std") {
		t.Errorf("base = %q", filepath.Base(got))
	}
}
