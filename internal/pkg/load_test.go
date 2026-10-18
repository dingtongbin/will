package pkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadModule(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "main.will"), "func Main() {\n}\n")
	write(t, filepath.Join(root, "lib", "util.will"), "func Util() {\n}\n")

	b := Load(root)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	if len(b.Diags) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(b.Diags))
	}
	if len(b.Packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(b.Packages))
	}
	if b.Packages[0].Path != "example.com/app" || b.Packages[0].Name != "app" || len(b.Packages[0].Files) != 1 {
		t.Errorf("package 0 = %+v", b.Packages[0])
	}
	if b.Packages[1].Path != "example.com/app/lib" || b.Packages[1].Name != "lib" {
		t.Errorf("package 1 = %+v", b.Packages[1])
	}
}

func TestModuleRootNameMismatch(t *testing.T) {
	root := filepath.Join(t.TempDir(), "other")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	b := Load(root)
	d := findCode(t, b.Diags, diag.CodePackageNameMismatch)
	if d.Message != "package name does not match the directory name" {
		t.Errorf("message = %q", d.Message)
	}
}

func TestInvalidPackageDirectoryName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "Bad", "x.will"), "func F() {\n}\n")
	b := Load(root)
	findCode(t, b.Diags, diag.CodePackageNameMismatch)
}

func TestMethodLeavesTypePackage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "type.will"), "struct T {\n    x: i32\n}\n")
	write(t, filepath.Join(root, "sub", "method.will"), "func (t: T) Move() {\n}\n")
	b := Load(root)
	d := findCode(t, b.Diags, diag.CodeMethodsTwoPackages)
	if d.Message != "methods of T are declared in two packages" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "a method can only be declared where its type is declared" {
		t.Errorf("help = %q", d.Help)
	}
	if !strings.HasSuffix(d.Pos.File, "method.will") {
		t.Errorf("position = %s, want the method file", d.Pos.File)
	}
}

func TestMethodInDeclaringPackageIsFine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "type.will"), "struct T {\n    x: i32\n}\n\nfunc (t: T) Move() {\n}\n")
	b := Load(root)
	for _, d := range b.Diags {
		if d.Code == diag.CodeMethodsTwoPackages {
			t.Fatalf("unexpected N0006:\n%s", diag.FormatAll(b.Diags))
		}
	}
}

func TestCrossPackageMemberResolution(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "api.will"), "func Exported() -> i32 {\n    return 1\n}\n\nfunc hidden() {\n}\n")
	write(t, filepath.Join(root, "sub", "use.will"), "import \"example.com/app\"\n\nfunc F() {\n    _ = app.Exported()\n}\n")
	b := Load(root)
	for _, d := range b.CheckAll() {
		if d.Code == diag.CodeUndefinedName || d.Code == diag.CodeUnusedImport {
			t.Fatalf("unexpected %s:\n%s", d.Code, diag.FormatAll(b.CheckAll()))
		}
	}
}

func TestCrossPackageMissingMember(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "api.will"), "func Exported() {\n}\n")
	write(t, filepath.Join(root, "sub", "use.will"), "import \"example.com/app\"\n\nfunc F() {\n    _ = app.Nope()\n}\n")
	ds := Load(root).CheckAll()
	d := findCode(t, ds, diag.CodeUndefinedName)
	if d.Pos.Line != 4 {
		t.Errorf("position = %d:%d, want line 4", d.Pos.Line, d.Pos.Col)
	}
}

func TestCrossPackagePrivateMember(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "api.will"), "func hidden() {\n}\n")
	write(t, filepath.Join(root, "sub", "use.will"), "import \"example.com/app\"\n\nfunc F() {\n    _ = app.hidden()\n}\n")
	findCode(t, Load(root).CheckAll(), diag.CodePrivateName)
}

func TestMissingWillMod(t *testing.T) {
	b := Load(filepath.Join(t.TempDir(), "nope"))
	if b.Err == nil {
		t.Fatal("Load succeeded without will.mod")
	}
}

func findCode(t *testing.T, ds []diag.Diagnostic, code string) diag.Diagnostic {
	t.Helper()
	for _, d := range ds {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("missing %s in:\n%s", code, diag.FormatAll(ds))
	return diag.Diagnostic{}
}
