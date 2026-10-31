package pkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/mod"
)

// writeModule writes one will.mod and its package files under dir. The module
// directive is the last segment of the directory name, which is the rule the
// package-name check applies.
func writeModule(t *testing.T, dir, modulePath string, files map[string]string) {
	t.Helper()
	write(t, filepath.Join(dir, "will.mod"), "module "+modulePath+"\nwill 1\n")
	for name, src := range files {
		write(t, filepath.Join(dir, filepath.FromSlash(name)), src)
	}
}

// requireReplace adds one require and the local replace that points at dir.
func requireReplace(t *testing.T, dir, dep, version, target string) {
	t.Helper()
	write(t, filepath.Join(dir, "will.mod"),
		"module "+modulePathOf(t, dir)+"\nwill 1\n"+
			"require "+dep+"@"+version+"\n"+
			"replace "+dep+"@"+version+" => "+target+"\n")
}

// modulePathOf reads the module path written in dir/will.mod.
func modulePathOf(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "will.mod"))
	if err != nil {
		t.Fatalf("read will.mod: %v", err)
	}
	m, ds := mod.Parse("will.mod", data)
	if m == nil {
		t.Fatalf("parse will.mod: %s", diag.FormatAll(ds))
	}
	return m.Path
}

// TestTwoModulesWithOnePathAreReported checks that two trees claiming the same
// module path are an error instead of a silent pick.
func TestTwoModulesWithOnePathAreReported(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	one := filepath.Join(parent, "one")
	two := filepath.Join(parent, "two")
	writeModule(t, one, "example.com/same", map[string]string{
		"one.will": "def one() -> i32 {\n    return 1\n}\n",
	})
	writeModule(t, two, "example.com/same", map[string]string{
		"two.will": "def two() -> i32 {\n    return 2\n}\n",
	})
	writeModule(t, app, "example.com/app", nil)
	write(t, filepath.Join(app, "will.mod"),
		"module example.com/app\nwill 1\n"+
			"require example.com/one@v1.0.0\n"+
			"replace example.com/one@v1.0.0 => ../one\n"+
			"require example.com/two@v1.0.0\n"+
			"replace example.com/two@v1.0.0 => ../two\n")
	write(t, filepath.Join(app, "main.will"), "def f() {\n}\n")

	b := Load(app)
	found := false
	for _, d := range b.Diags {
		if d.Code == diag.CodeInvalidWillMod && strings.Contains(d.Message, "example.com/same is declared by") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the duplicate module path was not reported:\n%s", diag.FormatAll(b.Diags))
	}
}

// TestAbsoluteReplaceTarget checks that a local replace may name an absolute
// directory, which the language server's mirror needs because it lives
// elsewhere.
func TestAbsoluteReplaceTarget(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	lib := filepath.Join(parent, "lib")
	writeModule(t, lib, "example.com/lib", map[string]string{
		"lib.will": "def double(x: i32) -> i32 {\n    return x * 2\n}\n",
	})
	writeModule(t, app, "example.com/app", nil)
	write(t, filepath.Join(app, "will.mod"),
		"module example.com/app\nwill 1\n"+
			"require example.com/lib@v1.0.0\n"+
			"replace example.com/lib@v1.0.0 => "+filepath.ToSlash(lib)+"\n")
	write(t, filepath.Join(app, "main.will"),
		"import \"example.com/lib\"\n\ndef f() -> i32 {\n    return lib.double(2)\n}\n")
	b := Load(app)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	if ds := b.CheckAll(); len(ds) != 0 {
		t.Fatalf("check diagnostics:\n%s", diag.FormatAll(ds))
	}
}

// TestReplaceChainCycleIsReported checks that a versioned replace loop is an
// error instead of a silent stop after the chain limit.
func TestReplaceChainCycleIsReported(t *testing.T) {
	app := filepath.Join(t.TempDir(), "app")
	writeModule(t, app, "example.com/app", nil)
	write(t, filepath.Join(app, "will.mod"),
		"module example.com/app\nwill 1\n"+
			"require example.com/a@v1.0.0\n"+
			"replace example.com/a@v1.0.0 => example.com/b@v1.0.0\n"+
			"replace example.com/b@v1.0.0 => example.com/a@v1.0.0\n")
	write(t, filepath.Join(app, "app.will"), "def f() {\n}\n")
	b := Load(app)
	for _, d := range b.Diags {
		if d.Code == diag.CodeInvalidWillMod && strings.Contains(d.Message, "replace chain for example.com/a") {
			return
		}
	}
	t.Fatalf("the chain cycle was not reported:\n%s", diag.FormatAll(b.Diags))
}

// TestLocalReplaceLoadsDependency checks the smallest module graph: the root
// requires one dependency, replaces it with a sibling directory, imports it,
// and the call into it type-checks.
func TestLocalReplaceLoadsDependency(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	lib := filepath.Join(parent, "lib")
	writeModule(t, lib, "example.com/lib", map[string]string{
		"lib.will": "def double(x: i32) -> i32 {\n    return x * 2\n}\n",
	})
	writeModule(t, app, "example.com/app", nil)
	requireReplace(t, app, "example.com/lib", "v1.0.0", "../lib")
	write(t, filepath.Join(app, "main.will"),
		"import \"example.com/lib\"\n\ndef f() -> i32 {\n    return lib.double(2)\n}\n")

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
	if len(b.Packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(b.Packages))
	}
	if b.Packages[0].Path != "example.com/lib" || b.Packages[1].Path != "example.com/app" {
		t.Fatalf("order = %q, %q; want the dependency first",
			b.Packages[0].Path, b.Packages[1].Path)
	}
}

// TestTransitiveReplaceLoadsWholeGraph checks that a dependency's own require
// and local replace are followed, with its relative path taken from the
// dependency's module root.
func TestTransitiveReplaceLoadsWholeGraph(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	a := filepath.Join(parent, "a")
	b := filepath.Join(parent, "b")
	writeModule(t, b, "example.com/b", map[string]string{
		"b.will": "def value() -> i32 {\n    return 3\n}\n",
	})
	writeModule(t, a, "example.com/a", map[string]string{
		"a.will": "import \"example.com/b\"\n\ndef value() -> i32 {\n    return b.value()\n}\n",
	})
	requireReplace(t, a, "example.com/b", "v1.0.0", "../b")
	writeModule(t, app, "example.com/app", map[string]string{
		"main.will": "import \"example.com/a\"\n\ndef f() -> i32 {\n    return a.value()\n}\n",
	})
	requireReplace(t, app, "example.com/a", "v1.0.0", "../a")

	bld := Load(app)
	if bld.Err != nil {
		t.Fatalf("load: %v", bld.Err)
	}
	if len(bld.Diags) != 0 {
		t.Fatalf("load diagnostics:\n%s", diag.FormatAll(bld.Diags))
	}
	if ds := bld.CheckAll(); len(ds) != 0 {
		t.Fatalf("check diagnostics:\n%s", diag.FormatAll(ds))
	}
	var paths []string
	for _, p := range bld.Packages {
		paths = append(paths, p.Path)
	}
	want := []string{"example.com/b", "example.com/a", "example.com/app"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("order = %v, want %v", paths, want)
	}
}

// TestHighestVersionWins checks that the highest require among all modules
// selects the local directory. The v1.0.0 target does not exist, so choosing
// it would leave the import unknown.
func TestHighestVersionWins(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	a := filepath.Join(parent, "a")
	lib := filepath.Join(parent, "lib")
	writeModule(t, lib, "example.com/lib", map[string]string{
		"lib.will": "def value() -> i32 {\n    return 7\n}\n",
	})
	writeModule(t, a, "example.com/a", map[string]string{
		"a.will": "import \"example.com/lib\"\n\ndef value() -> i32 {\n    return lib.value()\n}\n",
	})
	write(t, filepath.Join(a, "will.mod"),
		"module example.com/a\nwill 1\nrequire example.com/lib@v1.2.0\nreplace example.com/lib@v1.2.0 => ../lib\n")
	writeModule(t, app, "example.com/app", map[string]string{
		"main.will": "import \"example.com/a\"\n\ndef f() -> i32 {\n    return a.value()\n}\n",
	})
	write(t, filepath.Join(app, "will.mod"),
		"module example.com/app\nwill 1\n"+
			"require example.com/a@v1.0.0\nreplace example.com/a@v1.0.0 => ../a\n"+
			"require example.com/lib@v1.0.0\nreplace example.com/lib@v1.0.0 => ../missing\n")

	b := Load(app)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	if ds := b.CheckAll(); len(ds) != 0 {
		t.Fatalf("check diagnostics:\n%s", diag.FormatAll(ds))
	}
	found := false
	for _, p := range b.Packages {
		if p.Path == "example.com/lib" {
			found = true
		}
	}
	if !found {
		t.Fatalf("example.com/lib is missing from the graph")
	}
}

// TestSamePackageTwoPaths checks B0009: two replace keys point at one
// directory, so the same package is reachable under two import paths.
func TestSamePackageTwoPaths(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	shared := filepath.Join(parent, "shared")
	writeModule(t, shared, "example.com/shared", map[string]string{
		"shared.will": "def value() -> i32 {\n    return 1\n}\n",
	})
	writeModule(t, app, "example.com/app", nil)
	write(t, filepath.Join(app, "will.mod"),
		"module example.com/app\nwill 1\n"+
			"require example.com/a@v1.0.0\nreplace example.com/a@v1.0.0 => ../shared\n"+
			"require example.com/b@v1.0.0\nreplace example.com/b@v1.0.0 => ../shared\n")
	write(t, filepath.Join(app, "a.will"),
		"import \"example.com/a\"\n\ndef a() -> i32 {\n    return a.value()\n}\n")
	write(t, filepath.Join(app, "b.will"),
		"import \"example.com/b\"\n\ndef b() -> i32 {\n    return b.value()\n}\n")

	b := Load(app)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	d := findCode(t, b.Diags, diag.CodePackageTwoPaths)
	if d.Message != "package example.com/b is the same as example.com/a" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "import one of them; fix replace or the module layout" {
		t.Errorf("help = %q", d.Help)
	}
	if !strings.HasSuffix(d.Pos.File, "b.will") || d.Pos.Line != 1 {
		t.Errorf("position = %s, want the second import", d.Pos)
	}
}

// TestImportCycle checks B0010 between two modules: the message names the whole
// cycle and lands on the import that closes it.
func TestImportCycle(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	dep := filepath.Join(parent, "dep")
	writeModule(t, app, "example.com/app", map[string]string{
		"main.will": "import \"example.com/dep\"\n\ndef f() {\n    dep.g()\n}\n",
	})
	requireReplace(t, app, "example.com/dep", "v1.0.0", "../dep")
	writeModule(t, dep, "example.com/dep", map[string]string{
		"dep.will": "import \"example.com/app\"\n\ndef g() {\n    app.f()\n}\n",
	})
	requireReplace(t, dep, "example.com/app", "v1.0.0", "../app")

	b := Load(app)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	d := findCode(t, b.Diags, diag.CodeImportCycle)
	if d.Message != "import cycle: example.com/app -> example.com/dep -> example.com/app" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "break the cycle; move the shared part into a lower package" {
		t.Errorf("help = %q", d.Help)
	}
	if !strings.HasSuffix(d.Pos.File, "dep.will") || d.Pos.Line != 1 {
		t.Errorf("position = %s, want the import that closes the cycle", d.Pos)
	}
}

// TestThreePackageImportCycle checks B0010 on a cycle that runs through three
// packages.
func TestThreePackageImportCycle(t *testing.T) {
	parent := t.TempDir()
	a := filepath.Join(parent, "a")
	b := filepath.Join(parent, "b")
	c := filepath.Join(parent, "c")
	writeModule(t, a, "example.com/a", map[string]string{
		"a.will": "import \"example.com/b\"\n\ndef a() {\n    b.b()\n}\n",
	})
	requireReplace(t, a, "example.com/b", "v1.0.0", "../b")
	writeModule(t, b, "example.com/b", map[string]string{
		"b.will": "import \"example.com/c\"\n\ndef b() {\n    c.c()\n}\n",
	})
	requireReplace(t, b, "example.com/c", "v1.0.0", "../c")
	writeModule(t, c, "example.com/c", map[string]string{
		"c.will": "import \"example.com/a\"\n\ndef c() {\n    a.a()\n}\n",
	})
	requireReplace(t, c, "example.com/a", "v1.0.0", "../a")

	bld := Load(a)
	if bld.Err != nil {
		t.Fatalf("load: %v", bld.Err)
	}
	d := findCode(t, bld.Diags, diag.CodeImportCycle)
	want := "import cycle: example.com/a -> example.com/b -> example.com/c -> example.com/a"
	if d.Message != want {
		t.Errorf("message = %q, want %q", d.Message, want)
	}
	if !strings.HasSuffix(d.Pos.File, "c.will") {
		t.Errorf("position = %s, want the import in the last package", d.Pos)
	}
}

// TestSumMismatchOnLocalReplace checks B0003: a will.sum line for a local
// dependency is verified against the dependency's source face.
func TestSumMismatchOnLocalReplace(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	lib := filepath.Join(parent, "lib")
	writeModule(t, lib, "example.com/lib", map[string]string{
		"lib.will": "def value() {\n}\n",
	})
	writeModule(t, app, "example.com/app", map[string]string{
		"main.will": "import \"example.com/lib\"\n\ndef f() {\n    lib.value()\n}\n",
	})
	requireReplace(t, app, "example.com/lib", "v1.0.0", "../lib")
	write(t, filepath.Join(app, "will.sum"),
		"example.com/lib sha256:0000000000000000000000000000000000000000000000000000000000000000\n")

	b := Load(app)
	d := findCode(t, b.Diags, diag.CodeChecksumMismatch)
	if d.Message != "checksum mismatch for example.com/lib" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Help != "run will mod tidy" {
		t.Errorf("help = %q", d.Help)
	}
	if d.Pos.File != "will.sum" || d.Pos.Line != 1 {
		t.Errorf("position = %s, want will.sum:1", d.Pos)
	}

	// The correct source face hash is accepted, and a file outside the source
	// face does not change it.
	hash, err := mod.SourceFaceHash(lib)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	write(t, filepath.Join(app, "will.sum"), "example.com/lib sha256:"+hash+"\n")
	write(t, filepath.Join(lib, "README"), "notes\n")
	b = Load(app)
	for _, d := range b.Diags {
		if d.Code == diag.CodeChecksumMismatch {
			t.Fatalf("unexpected B0003:\n%s", diag.FormatAll(b.Diags))
		}
	}
}

// TestSumWithoutLineIsAccepted checks that a local dependency with no will.sum
// line is not a checksum error; will mod tidy adds the line.
func TestSumWithoutLineIsAccepted(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	lib := filepath.Join(parent, "lib")
	writeModule(t, lib, "example.com/lib", map[string]string{
		"lib.will": "def value() {\n}\n",
	})
	writeModule(t, app, "example.com/app", map[string]string{
		"main.will": "import \"example.com/lib\"\n\ndef f() {\n    lib.value()\n}\n",
	})
	requireReplace(t, app, "example.com/lib", "v1.0.0", "../lib")

	b := Load(app)
	for _, d := range b.Diags {
		if d.Code == diag.CodeChecksumMismatch {
			t.Fatalf("unexpected B0003 without a will.sum line:\n%s", diag.FormatAll(b.Diags))
		}
	}
}
