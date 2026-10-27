package pkg

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
)

// module writes a module whose package has the given files and returns its
// root.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	for name, src := range files {
		write(t, filepath.Join(root, filepath.FromSlash(name)), src)
	}
	return root
}

// TestSiblingFileNamesResolve checks that one package resolves as a unit: a
// name declared in one file is visible in another, private names included.
func TestSiblingFileNamesResolve(t *testing.T) {
	root := module(t, map[string]string{
		"a.will": "struct Box {\n    v: i64\n}\n\ndef value(b: Box) -> i64 {\n    return b.v\n}\n",
		"b.will": "def Use(b: Box) -> i64 {\n    return value(b)\n}\n",
	})
	ds := Load(root).CheckAll()
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want none:\n%s", len(ds), diag.FormatAll(ds))
	}
}

// TestSiblingFileTypesAreChecked checks that the declaration environment is
// package-wide: a field error on a type declared in another file is caught.
func TestSiblingFileTypesAreChecked(t *testing.T) {
	root := module(t, map[string]string{
		"a.will": "struct Box {\n    v: i64\n}\n",
		"b.will": "def Use(b: Box) -> i64 {\n    return b.w\n}\n",
	})
	ds := Load(root).CheckAll()
	d := findCode(t, ds, diag.CodeNoField)
	if d.Message != "no field w on Box" {
		t.Errorf("message = %q", d.Message)
	}
}

// TestCrossFileDuplicateName checks that two declarations with the same name
// in one package are an error even when they are written in different files,
// and that it is reported once.
func TestCrossFileDuplicateName(t *testing.T) {
	root := module(t, map[string]string{
		"a.will": "def F() {\n}\n",
		"b.will": "def F() {\n}\n",
	})
	ds := Load(root).CheckAll()
	d := findCode(t, ds, diag.CodeDuplicateName)
	if d.Message != "duplicate top-level name F" {
		t.Errorf("message = %q", d.Message)
	}
	count := 0
	for _, x := range ds {
		if x.Code == diag.CodeDuplicateName {
			count++
		}
	}
	if count != 1 {
		t.Errorf("duplicate name reported %d times, want 1:\n%s", count, diag.FormatAll(ds))
	}
	if !strings.HasSuffix(d.Pos.File, "b.will") {
		t.Errorf("position = %s, want the second declaration", d.Pos)
	}
}
