package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeWillcRoundTrip(t *testing.T) {
	paths := []string{"a.will", "lib/b.will"}
	texts := []string{"#1 module path=\"m\"\n#2 end\n", "#1 module path=\"m\"\n#2 end\n#3 end\n"}
	blob := encodeWillc(paths, texts)
	gotPaths, gotTexts, ok := decodeWillc(blob)
	if !ok {
		t.Fatal("decode failed")
	}
	if !samePaths(gotPaths, paths) {
		t.Fatalf("paths = %v, want %v", gotPaths, paths)
	}
	for i := range texts {
		if gotTexts[i] != texts[i] {
			t.Fatalf("text %d = %q, want %q", i, gotTexts[i], texts[i])
		}
	}
	if _, _, ok := decodeWillc(blob[:len(blob)-1]); ok {
		t.Fatal("a truncated blob was accepted")
	}
}

func TestFrontEndCacheHitsOnTheSecondRun(t *testing.T) {
	root := filepath.Join(t.TempDir(), "front")
	t.Setenv("WILL_CACHE", t.TempDir())
	writeTree(t, root, map[string]string{
		"will.mod":     "module example.com/front\nwill 1\n",
		"main.will":    "import \"example.com/front/lib\"\n\nfunc main() -> i32 {\n    return lib.Double(2)\n}\n",
		"lib/lib.will": "func Double(n: i32) -> i32 {\n    return n + n\n}\n",
	})

	frontEndStats.hits, frontEndStats.misses = 0, 0
	code, first, errOut := runCLI("emit-llvm", root)
	if code != 0 {
		t.Fatalf("first emit: code = %d, stdout = %q, stderr = %q", code, first, errOut)
	}
	if frontEndStats.hits != 0 || frontEndStats.misses != 2 {
		t.Fatalf("first run: hits = %d, misses = %d, want 0 and 2", frontEndStats.hits, frontEndStats.misses)
	}

	frontEndStats.hits, frontEndStats.misses = 0, 0
	code, second, errOut := runCLI("emit-llvm", root)
	if code != 0 {
		t.Fatalf("second emit: code = %d, stderr = %q", code, errOut)
	}
	if frontEndStats.hits != 2 || frontEndStats.misses != 0 {
		t.Fatalf("second run: hits = %d, misses = %d, want 2 and 0", frontEndStats.hits, frontEndStats.misses)
	}
	if first != second {
		t.Fatal("the cached products lowered to different text")
	}
}

func TestFrontEndCacheInvalidatesTheDependent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "front")
	t.Setenv("WILL_CACHE", t.TempDir())
	writeTree(t, root, map[string]string{
		"will.mod":     "module example.com/front\nwill 1\n",
		"main.will":    "import \"example.com/front/lib\"\n\nfunc main() -> i32 {\n    return lib.Double(2)\n}\n",
		"lib/lib.will": "func Double(n: i32) -> i32 {\n    return n + n\n}\n",
	})
	frontEndStats.hits, frontEndStats.misses = 0, 0
	if code, out, errOut := runCLI("emit-llvm", root); code != 0 {
		t.Fatalf("first emit: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}

	writeTree(t, root, map[string]string{
		"lib/lib.will": "func Double(n: i32) -> i32 {\n    return n + n + 1\n}\n",
	})
	frontEndStats.hits, frontEndStats.misses = 0, 0
	if code, _, errOut := runCLI("emit-llvm", root); code != 0 {
		t.Fatalf("second emit: code = %d, stderr = %q", code, errOut)
	}
	if frontEndStats.hits != 0 || frontEndStats.misses != 2 {
		t.Fatalf("after a dependency change: hits = %d, misses = %d, want 0 and 2",
			frontEndStats.hits, frontEndStats.misses)
	}

	frontEndStats.hits, frontEndStats.misses = 0, 0
	if code, _, errOut := runCLI("emit-llvm", root); code != 0 {
		t.Fatalf("third emit: code = %d, stderr = %q", code, errOut)
	}
	if frontEndStats.hits != 2 || frontEndStats.misses != 0 {
		t.Fatalf("third run: hits = %d, misses = %d, want 2 and 0", frontEndStats.hits, frontEndStats.misses)
	}
}

func TestFrontEndCacheCoversAnAliasImport(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	shared := filepath.Join(parent, "shared")
	t.Setenv("WILL_CACHE", t.TempDir())
	writeTree(t, root, map[string]string{
		"will.mod":  "module example.com/app\nwill 1\nrequire example.com/lib@v1.0.0\nreplace example.com/lib@v1.0.0 => ../shared\n",
		"main.will": "import \"example.com/lib\"\n\nfunc main() -> i32 {\n    return lib.Double(2)\n}\n",
	})
	writeTree(t, shared, map[string]string{
		"will.mod":    "module example.com/shared\nwill 1\n",
		"shared.will": "func Double(n: i32) -> i32 {\n    return n + n\n}\n",
	})

	frontEndStats.hits, frontEndStats.misses = 0, 0
	code, first, errOut := runCLI("emit-llvm", root)
	if code != 0 {
		t.Fatalf("first emit: code = %d, stdout = %q, stderr = %q", code, first, errOut)
	}
	if frontEndStats.misses != 2 {
		t.Fatalf("first run: misses = %d, want 2", frontEndStats.misses)
	}
	frontEndStats.hits, frontEndStats.misses = 0, 0
	code, second, errOut := runCLI("emit-llvm", root)
	if code != 0 {
		t.Fatalf("second emit: code = %d, stderr = %q", code, errOut)
	}
	if frontEndStats.hits != 2 || frontEndStats.misses != 0 {
		t.Fatalf("second run: hits = %d, misses = %d, want 2 and 0", frontEndStats.hits, frontEndStats.misses)
	}
	if first != second {
		t.Fatal("the cached products lowered to different text")
	}
}

func TestCacheEnvUsageErrorsExitTwo(t *testing.T) {
	t.Setenv("WILL_CACHE", "relative/cache")
	code, _, errOut := runCLI("version")
	if code != 2 || !strings.Contains(errOut, "WILL_CACHE must be an absolute path") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}

	t.Setenv("WILL_CACHE", t.TempDir())
	t.Setenv("WILL_CACHE_MAX", "1.5G")
	code, _, errOut = runCLI("version")
	if code != 2 || !strings.Contains(errOut, "WILL_CACHE_MAX") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}
