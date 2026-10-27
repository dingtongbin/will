package mod

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func hash(t *testing.T, root string) string {
	t.Helper()
	sum, err := SourceFaceHash(root)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return sum
}

func TestSourceFaceHashIsStable(t *testing.T) {
	root := t.TempDir()
	write(t, root, "will.mod", "module example.com/app\nwill 1\n")
	write(t, root, "src/a.will", "def F() {\n}\n")
	write(t, root, "src/native.h", "int puts(const char *);\n")
	first := hash(t, root)
	if second := hash(t, root); first != second {
		t.Fatalf("two hashes differ: %s, %s", first, second)
	}
}

func TestSourceFaceHashFollowsTheFace(t *testing.T) {
	base := t.TempDir()
	write(t, base, "will.mod", "module example.com/app\nwill 1\n")
	write(t, base, "a.will", "def F() {\n}\n")
	before := hash(t, base)

	// A source file, a package header, and the module file are all in the face.
	cases := []struct {
		name string
		path string
		data string
	}{
		{"source", "b.will", "def G() {\n}\n"},
		{"header", "pkg/native.h", "int atoi(const char *);\n"},
		{"module", "will.mod", "module example.com/app\nwill 2\n"},
		{"sum", "will.sum", "example.com/lib sha256:00\n"},
		{"readme", "README", "notes\n"},
		{"corpus", "tests/case/helper.will", "def Helper() {\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "will.mod", "module example.com/app\nwill 1\n")
			write(t, root, "a.will", "def F() {\n}\n")
			if got := hash(t, root); got != before {
				t.Fatalf("a fresh copy hashed differently")
			}
			write(t, root, c.path, c.data)
			changed := hash(t, root) != before
			inFace := c.name == "source" || c.name == "header" || c.name == "module"
			if changed != inFace {
				t.Fatalf("%s: changed = %v, want %v", c.name, changed, inFace)
			}
		})
	}
}

func TestParseSumRejectsBadLines(t *testing.T) {
	lines, ds := ParseSum("will.sum", []byte("example.com/lib sha256:abc\nbad line here\n"))
	if len(lines) != 0 {
		t.Fatalf("lines = %d, want 0", len(lines))
	}
	if len(ds) != 2 || ds[0].Code != "B0003" || ds[1].Code != "B0003" {
		t.Fatalf("diagnostics = %+v", ds)
	}
	if ds[0].Message != "checksum mismatch for example.com/lib" {
		t.Errorf("message = %q", ds[0].Message)
	}
}

func TestParseSumAcceptsAFullSHA256(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	lines, ds := ParseSum("will.sum", []byte("example.com/lib sha256:"+digest+"\n"))
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %+v", ds)
	}
	if len(lines) != 1 || lines[0].Sum != "sha256:"+digest {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestFormatSumSortsByPath(t *testing.T) {
	out := FormatSum([]SumLine{
		{Path: "b/lib", Sum: "sha256:2"},
		{Path: "a/lib", Sum: "sha256:1"},
	})
	if string(out) != "a/lib sha256:1\nb/lib sha256:2\n" {
		t.Fatalf("out = %q", out)
	}
}
