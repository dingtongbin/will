package pkg

import (
	"path/filepath"
	"testing"

	"github.com/will-lang/will/internal/diag"
)

// TestWalkModuleDefersNameTables keeps header resolution and the name tables
// out of the module walk. Building the tables there made every unimported
// standard-library package ask clang about its C headers before the pruning
// pass could drop it.
func TestWalkModuleDefersNameTables(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "main.will"), "def Main() {\n}\n")

	pkgs, ds := walkModule(&moduleNode{Dir: root, Path: "example.com/app"})
	if len(ds) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
	if len(pkgs) != 1 {
		t.Fatalf("packages = %d, want 1", len(pkgs))
	}
	if len(pkgs[0].Names) != 0 || len(pkgs[0].Exports) != 0 {
		t.Fatalf("walkModule built name tables: names=%d exports=%d",
			len(pkgs[0].Names), len(pkgs[0].Exports))
	}
}
