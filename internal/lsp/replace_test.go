package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAnalyzeTextFollowsALocalReplace checks that the module mirror keeps a
// local replace working. The mirror lives in another directory, so a relative
// target such as ../dep would otherwise point at nothing and the import would
// be reported as unknown.
func TestAnalyzeTextFollowsALocalReplace(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	dep := filepath.Join(parent, "dep")
	writeText(t, app, map[string]string{
		"will.mod": "module example.com/app\nwill 1\n" +
			"require example.com/dep@v1.0.0\n" +
			"replace example.com/dep@v1.0.0 => ../dep\n",
		"app.will": "import \"example.com/dep\"\n\nfunc F() -> i32 {\n    return dep.Double(2)\n}\n",
	})
	writeText(t, dep, map[string]string{
		"will.mod": "module example.com/dep\nwill 1\n",
		"dep.will": "func Double(n: i32) -> i32 {\n    return n + n\n}\n",
	})
	path := filepath.Join(app, "app.will")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ds, err := analyzeText(path, string(body))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %d, want 0: %+v", len(ds), ds)
	}
}

func writeText(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}
