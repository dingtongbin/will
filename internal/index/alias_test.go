package index

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/pkg"
)

func TestIndexTypeAlias(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "api.will"), "type UserId = i64\n\ndef load(id: UserId) -> i64 {\n    return id\n}\n")
	text := string(Build(pkg.Load(root)))
	if !strings.Contains(text, `decl file="api.will" line=1 col=1 kind="typealias" name="UserId" vis="public"`) {
		t.Fatalf("index lacks the alias declaration:\n%s", text)
	}
	if !strings.Contains(text, `alias file="api.will" line=1 col=6 name="UserId" target="i64"`) {
		t.Fatalf("index lacks the alias target:\n%s", text)
	}
}

func TestIndexVisibilityUsesUnderscorePrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	write(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	write(t, filepath.Join(root, "api.will"), "def visible() {}\n\ndef __hidden() {}\n")
	text := string(Build(pkg.Load(root)))
	if !strings.Contains(text, `kind="func" name="visible" vis="public"`) {
		t.Fatalf("visible function is not public:\n%s", text)
	}
	if !strings.Contains(text, `kind="func" name="__hidden" vis="private"`) {
		t.Fatalf("__hidden function is not private:\n%s", text)
	}
}
