package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/pkg"
)

const (
	fixtureWillMod    = "module example.com/app\nwill 1\n"
	fixtureMainWill   = "func main() {}\n"
	fixtureSecondWill = "func Second() {}\n"
	fixtureLibWill    = "func Helper() -> i32 {\n    return 1\n}\n"
	fixtureHeader     = "#define WILL_NATIVE 1\n"
	fixtureCSource    = "int will_lib_native(void) { return 1; }\n"
	fixtureSum        = "example.com/dep sha256:0000000000000000000000000000000000000000000000000000000000000000\n"
)

// moduleFixture is one loaded module tree on disk.
type moduleFixture struct {
	root  string
	build *pkg.Build
}

// baseCheckFlags is the flag set every key test starts from.
func baseCheckFlags() checkFlags {
	return checkFlags{opt: "-O2", disabled: map[string]bool{}}
}

// writeCacheFixture writes one fixture file and creates its parent directory.
func writeCacheFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newModuleFixture builds a module with a root package, a subpackage, a C
// source, and a header, so every part the directory key reads is present.
func newModuleFixture(t *testing.T, tweak func(root string)) moduleFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app")
	writeCacheFixture(t, filepath.Join(root, "will.mod"), fixtureWillMod)
	writeCacheFixture(t, filepath.Join(root, "will.sum"), fixtureSum)
	writeCacheFixture(t, filepath.Join(root, "main.will"), fixtureMainWill)
	writeCacheFixture(t, filepath.Join(root, "second.will"), fixtureSecondWill)
	writeCacheFixture(t, filepath.Join(root, "lib", "lib.will"), fixtureLibWill)
	writeCacheFixture(t, filepath.Join(root, "native.h"), fixtureHeader)
	writeCacheFixture(t, filepath.Join(root, "lib", "native.c"), fixtureCSource)
	if tweak != nil {
		tweak(root)
	}
	b := pkg.Load(root)
	if b.Err != nil {
		t.Fatalf("load fixture module: %v", b.Err)
	}
	if b.Mod == nil {
		t.Fatalf("fixture module did not parse: %v", b.Diags)
	}
	return moduleFixture{root: root, build: b}
}

// fileCacheKeyForTest requires the file key to be available and well formed.
func fileCacheKeyForTest(t *testing.T, files []string, flags checkFlags) string {
	t.Helper()
	store, key, contents := checkCache(files, flags)
	if store == nil {
		t.Fatal("checkCache returned no store; WILL_CACHE is not set")
	}
	if key == "" {
		t.Fatal("checkCache returned an empty key")
	}
	if len(contents) != len(files) {
		t.Fatalf("checkCache read %d files, want %d", len(contents), len(files))
	}
	return key
}

// dirCacheKeyForTest requires the directory key to be available.
func dirCacheKeyForTest(t *testing.T, b *pkg.Build, flags checkFlags) string {
	t.Helper()
	store, key, cacheable := dirCacheKey(b, flags)
	if store == nil || !cacheable || key == "" {
		t.Fatalf("dirCacheKey = (%v, %q, %v), want a cacheable key", store, key, cacheable)
	}
	return key
}

// loweringCacheKeyForTest requires the lowering key to be available.
func loweringCacheKeyForTest(t *testing.T, texts []string, opts buildOptions) string {
	t.Helper()
	store, key := loweringCacheKey(texts, opts)
	if store == nil || key == "" {
		t.Fatalf("loweringCacheKey = (%v, %q), want a key", store, key)
	}
	return key
}

// requireKeyDigest pins the key shape: one lowercase hexadecimal sha256.
func requireKeyDigest(t *testing.T, key string) {
	t.Helper()
	if len(key) != 64 {
		t.Fatalf("key length = %d, want 64: %q", len(key), key)
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		t.Fatalf("key contains %q at %d: %q", c, i, key)
	}
}

// otherTarget returns a supported target that is not the host target, so a
// target change is observable on every supported host.
func otherTarget(t *testing.T) string {
	t.Helper()
	host := targetTriple()
	for _, target := range supportedTargets {
		if target != host {
			return target
		}
	}
	t.Fatal("no supported target differs from the host target")
	return ""
}

// assertKeyChanged fails when a changed input kept the old key.
func assertKeyChanged(t *testing.T, base, changed, label string) {
	t.Helper()
	if changed == base {
		t.Fatalf("%s did not change the key %s", label, base)
	}
}

// TestFileCacheKeyInvariants pins the file-level check key: the same file set
// gives the same key, and every input the key reads changes it.
func TestFileCacheKeyInvariants(t *testing.T) {
	t.Setenv("WILL_CACHE", t.TempDir())
	dir := t.TempDir()
	first := filepath.Join(dir, "first.will")
	second := filepath.Join(dir, "second.will")
	writeCacheFixture(t, first, "func First() {}\n")
	writeCacheFixture(t, second, "func Second() {}\n")
	files := []string{first, second}
	flags := baseCheckFlags()

	base := fileCacheKeyForTest(t, files, flags)
	requireKeyDigest(t, base)
	if again := fileCacheKeyForTest(t, files, flags); again != base {
		t.Fatalf("the same inputs gave %s then %s", base, again)
	}

	t.Run("source bytes", func(t *testing.T) {
		writeCacheFixture(t, first, "func First() -> i32 { return 1 }\n")
		t.Cleanup(func() { writeCacheFixture(t, first, "func First() {}\n") })
		assertKeyChanged(t, base, fileCacheKeyForTest(t, files, flags), "source bytes")
	})
	t.Run("argument order", func(t *testing.T) {
		reordered := []string{second, first}
		assertKeyChanged(t, base, fileCacheKeyForTest(t, reordered, flags), "argument order")
	})
	t.Run("file path", func(t *testing.T) {
		renamed := filepath.Join(dir, "renamed.will")
		writeCacheFixture(t, renamed, "func First() {}\n")
		t.Cleanup(func() { _ = os.Remove(renamed) })
		assertKeyChanged(t, base, fileCacheKeyForTest(t, []string{renamed, second}, flags), "file path")
	})
	t.Run("added file", func(t *testing.T) {
		third := filepath.Join(dir, "third.will")
		writeCacheFixture(t, third, "func Third() {}\n")
		t.Cleanup(func() { _ = os.Remove(third) })
		assertKeyChanged(t, base, fileCacheKeyForTest(t, []string{first, second, third}, flags), "an added file")
	})

	target := otherTarget(t)
	variants := []struct {
		name   string
		change func(*checkFlags)
	}{
		{"target", func(f *checkFlags) { f.target = target }},
		{"optimization level", func(f *checkFlags) { f.opt = "-O0" }},
		{"freestanding", func(f *checkFlags) { f.freestanding = true }},
		{"features", func(f *checkFlags) { f.features = "tls" }},
		{"json", func(f *checkFlags) { f.json = true }},
		{"werror", func(f *checkFlags) { f.werror = true }},
		{"disabled warning family", func(f *checkFlags) { f.disabled = map[string]bool{"shadow": true} }},
	}
	for _, variant := range variants {
		variant := variant
		t.Run(variant.name, func(t *testing.T) {
			changed := flags
			variant.change(&changed)
			assertKeyChanged(t, base, fileCacheKeyForTest(t, files, changed), variant.name)
		})
	}
}

// TestDirectoryCacheKeyInvariants pins the directory key: identical module
// trees under different roots give one key, and every input the key reads
// changes it.
func TestDirectoryCacheKeyInvariants(t *testing.T) {
	t.Setenv("WILL_CACHE", t.TempDir())
	control := newModuleFixture(t, nil)
	flags := baseCheckFlags()
	base := dirCacheKeyForTest(t, control.build, flags)
	requireKeyDigest(t, base)
	if again := dirCacheKeyForTest(t, control.build, flags); again != base {
		t.Fatalf("the same module gave %s then %s", base, again)
	}

	t.Run("unrelated root path", func(t *testing.T) {
		other := newModuleFixture(t, nil)
		if got := dirCacheKeyForTest(t, other.build, flags); got != base {
			t.Fatalf("identical trees under different roots gave %s and %s", base, got)
		}
	})
	t.Run("source bytes", func(t *testing.T) {
		changed := newModuleFixture(t, func(root string) {
			writeCacheFixture(t, filepath.Join(root, "main.will"), "func main() -> i32 {\n    return 0\n}\n")
		})
		assertKeyChanged(t, base, dirCacheKeyForTest(t, changed.build, flags), "source bytes")
	})
	t.Run("header bytes", func(t *testing.T) {
		changed := newModuleFixture(t, func(root string) {
			writeCacheFixture(t, filepath.Join(root, "native.h"), "#define WILL_NATIVE 2\n")
		})
		assertKeyChanged(t, base, dirCacheKeyForTest(t, changed.build, flags), "header bytes")
	})
	t.Run("c source bytes", func(t *testing.T) {
		changed := newModuleFixture(t, func(root string) {
			writeCacheFixture(t, filepath.Join(root, "lib", "native.c"), "int will_lib_native(void) { return 2; }\n")
		})
		assertKeyChanged(t, base, dirCacheKeyForTest(t, changed.build, flags), "C source bytes")
	})
	t.Run("will.sum bytes", func(t *testing.T) {
		changed := newModuleFixture(t, func(root string) {
			writeCacheFixture(t, filepath.Join(root, "will.sum"),
				"example.com/dep sha256:1111111111111111111111111111111111111111111111111111111111111111\n")
		})
		assertKeyChanged(t, base, dirCacheKeyForTest(t, changed.build, flags), "will.sum bytes")
	})
	t.Run("will.mod bytes", func(t *testing.T) {
		changed := newModuleFixture(t, func(root string) {
			writeCacheFixture(t, filepath.Join(root, "will.mod"), fixtureWillMod+"feature tls\n")
		})
		assertKeyChanged(t, base, dirCacheKeyForTest(t, changed.build, flags), "will.mod bytes")
	})
	t.Run("missing will.sum", func(t *testing.T) {
		changed := newModuleFixture(t, func(root string) {
			if err := os.Remove(filepath.Join(root, "will.sum")); err != nil {
				t.Fatalf("remove will.sum: %v", err)
			}
		})
		assertKeyChanged(t, base, dirCacheKeyForTest(t, changed.build, flags), "a missing will.sum")
	})

	target := otherTarget(t)
	variants := []struct {
		name   string
		change func(*checkFlags)
	}{
		{"target", func(f *checkFlags) { f.target = target }},
		{"optimization level", func(f *checkFlags) { f.opt = "-O0" }},
		{"freestanding", func(f *checkFlags) { f.freestanding = true }},
		{"features", func(f *checkFlags) { f.features = "tls" }},
		{"json", func(f *checkFlags) { f.json = true }},
		{"werror", func(f *checkFlags) { f.werror = true }},
		{"disabled warning family", func(f *checkFlags) { f.disabled = map[string]bool{"shadow": true} }},
	}
	for _, variant := range variants {
		variant := variant
		t.Run(variant.name, func(t *testing.T) {
			changed := flags
			variant.change(&changed)
			assertKeyChanged(t, base, dirCacheKeyForTest(t, control.build, changed), variant.name)
		})
	}
}

// TestDirectoryCacheKeyFollowsLoadedOrder pins that the key follows the order
// of packages and files as loaded, so the same set in another order keys
// differently.
func TestDirectoryCacheKeyFollowsLoadedOrder(t *testing.T) {
	t.Setenv("WILL_CACHE", t.TempDir())
	fixture := newModuleFixture(t, nil)
	flags := baseCheckFlags()
	base := dirCacheKeyForTest(t, fixture.build, flags)

	reordered := *fixture.build
	reordered.Packages = make([]*pkg.Package, len(fixture.build.Packages))
	for i, p := range fixture.build.Packages {
		copied := *p
		copied.Files = append([]*pkg.File(nil), p.Files...)
		for left, right := 0, len(copied.Files)-1; left < right; left, right = left+1, right-1 {
			copied.Files[left], copied.Files[right] = copied.Files[right], copied.Files[left]
		}
		reordered.Packages[len(fixture.build.Packages)-1-i] = &copied
	}
	assertKeyChanged(t, base, dirCacheKeyForTest(t, &reordered, flags), "package and file order")
}

// TestLoweringCacheKeyInvariants pins the lowering key: the same willc texts
// give the same key, and every input the key reads changes it.
func TestLoweringCacheKeyInvariants(t *testing.T) {
	t.Setenv("WILL_CACHE", t.TempDir())
	opts := buildOptions{entry: "main", opt: "-O2", disabled: map[string]bool{}}
	texts := []string{"willc of app", "willc of lib"}
	base := loweringCacheKeyForTest(t, texts, opts)
	requireKeyDigest(t, base)
	if again := loweringCacheKeyForTest(t, texts, opts); again != base {
		t.Fatalf("the same texts gave %s then %s", base, again)
	}

	t.Run("package willc text", func(t *testing.T) {
		changed := []string{texts[0], "willc of lib, edited"}
		assertKeyChanged(t, base, loweringCacheKeyForTest(t, changed, opts), "a package willc text")
	})
	t.Run("willc order", func(t *testing.T) {
		reordered := []string{texts[1], texts[0]}
		assertKeyChanged(t, base, loweringCacheKeyForTest(t, reordered, opts), "willc order")
	})
	t.Run("added package", func(t *testing.T) {
		changed := append(append([]string(nil), texts...), "willc of dep")
		assertKeyChanged(t, base, loweringCacheKeyForTest(t, changed, opts), "an added package")
	})
	t.Run("removed package", func(t *testing.T) {
		changed := texts[:1]
		assertKeyChanged(t, base, loweringCacheKeyForTest(t, changed, opts), "a removed package")
	})

	target := otherTarget(t)
	variants := []struct {
		name   string
		change func(*buildOptions)
	}{
		{"entry name", func(o *buildOptions) { o.entry = "start" }},
		{"optimization level", func(o *buildOptions) { o.opt = "-O0" }},
		{"target", func(o *buildOptions) { o.target = target }},
		{"library output", func(o *buildOptions) { o.lib = true }},
		{"debug info", func(o *buildOptions) { o.debug = true }},
		{"freestanding", func(o *buildOptions) { o.freestanding = true }},
		{"features", func(o *buildOptions) { o.features = "tls" }},
		{"disabled warning family", func(o *buildOptions) { o.disabled = map[string]bool{"shadow": true} }},
	}
	for _, variant := range variants {
		variant := variant
		t.Run(variant.name, func(t *testing.T) {
			changed := opts
			variant.change(&changed)
			assertKeyChanged(t, base, loweringCacheKeyForTest(t, texts, changed), variant.name)
		})
	}
}

// TestCacheKeysIncludeCompilerIdentity pins that all three keys change when
// the compiler hash changes. The key is a digest, so identity is observable
// through sensitivity rather than through a substring of the key.
func TestCacheKeysIncludeCompilerIdentity(t *testing.T) {
	t.Setenv("WILL_CACHE", t.TempDir())
	if got := compilerIdentity(); !strings.HasPrefix(got, "will "+CompilerVersion) {
		t.Fatalf("compiler identity %q does not start with the will version prefix", got)
	}

	fixture := newModuleFixture(t, nil)
	flags := baseCheckFlags()
	opts := buildOptions{entry: "main", opt: "-O2", disabled: map[string]bool{}}
	files := []string{filepath.Join(fixture.root, "main.will")}
	texts := []string{"willc of app"}

	baseCheck := fileCacheKeyForTest(t, files, flags)
	baseDir := dirCacheKeyForTest(t, fixture.build, flags)
	baseLower := loweringCacheKeyForTest(t, texts, opts)

	setCompilerHash(t, "identity-a")
	if got := compilerIdentity(); !strings.Contains(got, "identity-a") {
		t.Fatalf("test compiler identity was not installed: %q", got)
	}
	firstCheck := fileCacheKeyForTest(t, files, flags)
	firstDir := dirCacheKeyForTest(t, fixture.build, flags)
	firstLower := loweringCacheKeyForTest(t, texts, opts)

	setCompilerHash(t, "identity-b")
	secondCheck := fileCacheKeyForTest(t, files, flags)
	secondDir := dirCacheKeyForTest(t, fixture.build, flags)
	secondLower := loweringCacheKeyForTest(t, texts, opts)

	cases := []struct {
		name   string
		base   string
		first  string
		second string
	}{
		{"file key", baseCheck, firstCheck, secondCheck},
		{"directory key", baseDir, firstDir, secondDir},
		{"lowering key", baseLower, firstLower, secondLower},
	}
	for _, tc := range cases {
		if tc.base == tc.first {
			t.Errorf("%s ignored the compiler identity", tc.name)
		}
		if tc.first == tc.second {
			t.Errorf("%s ignored a changed compiler identity", tc.name)
		}
	}
}

// setCompilerHash installs one compiler hash for the rest of the test. The
// once in binaryHash is spent first, so later reads see the assignment.
func setCompilerHash(t *testing.T, hash string) {
	t.Helper()
	binaryHash()
	previous := binaryHashSum
	binaryHashSum = hash
	t.Cleanup(func() { binaryHashSum = previous })
}

// TestCacheKeysRequireWILLCache pins that no key is produced without a cache
// root, so a run without WILL_CACHE never touches a cache directory.
func TestCacheKeysRequireWILLCache(t *testing.T) {
	t.Setenv("WILL_CACHE", "")
	fixture := newModuleFixture(t, nil)
	flags := baseCheckFlags()
	files := []string{filepath.Join(fixture.root, "main.will")}

	if store, key, contents := checkCache(files, flags); store != nil || key != "" || contents != nil {
		t.Fatalf("checkCache without WILL_CACHE = (%v, %q, %v)", store, key, contents)
	}
	if store, key, cacheable := dirCacheKey(fixture.build, flags); store != nil || key != "" || cacheable {
		t.Fatalf("dirCacheKey without WILL_CACHE = (%v, %q, %v)", store, key, cacheable)
	}
	if store, key := loweringCacheKey([]string{"willc"}, buildOptions{entry: "main"}); store != nil || key != "" {
		t.Fatalf("loweringCacheKey without WILL_CACHE = (%v, %q)", store, key)
	}
}
