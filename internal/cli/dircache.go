package cli

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/pkg"
)

// runCachedBuild checks a loaded module, using the directory key when the
// cache is on.
func runCachedBuild(b *pkg.Build, jsonOut, werror bool, stdout io.Writer) int {
	store, key, cacheable := dirCacheKey(b, jsonOut, werror)
	if cacheable {
		if data, hit := store.Get(key, cache.Diag); hit {
			out, code := decodeDiag(data[cache.Diag])
			stdout.Write(out)
			return code
		}
	}
	ds := b.CheckAll()
	build := buildIdentity()
	var out []byte
	if jsonOut {
		out = []byte(diag.JSONAll(ds, build))
	} else {
		out = []byte(diag.FormatAll(ds))
	}
	stdout.Write(out)
	code := exitCode(ds, werror)
	if cacheable {
		_ = store.Put(key, map[string][]byte{cache.Diag: encodeDiag(code, out)})
		_ = store.Trim()
	}
	return code
}

func exitCode(ds []diag.Diagnostic, werror bool) int {
	code := 0
	for _, d := range ds {
		if d.Severity == diag.Error {
			code = 1
		}
		if werror && d.Severity == diag.Warning {
			code = 1
		}
	}
	return code
}

// dirCacheKey builds the key of one module directory run. The key contains
// everything the run reads: module files, the module and sum files, every
// in-package C or H file, the exported names of every package, the compiler
// identity, and the flags.
func dirCacheKey(b *pkg.Build, jsonOut, werror bool) (*cache.Store, string, bool) {
	root := os.Getenv("WILL_CACHE")
	if root == "" || b.Mod == nil {
		return nil, "", false
	}
	h := cache.NewHasher()
	h.AddStrings("module", b.Mod.Path, strconv.Itoa(b.Mod.Will))
	if data, err := os.ReadFile(filepath.Join(b.Root, "will.mod")); err == nil {
		h.Add("will.mod", data)
	}
	if data, err := os.ReadFile(filepath.Join(b.Root, "will.sum")); err == nil {
		h.Add("will.sum", data)
	}
	for _, path := range headerFiles(b.Root) {
		rel, err := filepath.Rel(b.Root, path)
		if err != nil {
			continue
		}
		if data, err := os.ReadFile(path); err == nil {
			h.Add(filepath.ToSlash(rel), data)
		}
	}
	for _, p := range b.Packages {
		names := make([]string, 0, len(p.Exports))
		for name := range p.Exports {
			names = append(names, name)
		}
		sort.Strings(names)
		h.AddStrings("exports", append([]string{p.Path}, names...)...)
		for _, f := range p.Files {
			if data, err := os.ReadFile(filepath.Join(p.Dir, f.Path)); err == nil {
				h.Add(p.Path+"/"+f.Path, data)
			}
		}
	}
	build := buildIdentity()
	h.AddStrings("compiler", build.Compiler, build.Target, build.Opt)
	h.AddStrings("flags", fmt.Sprintf("json=%v", jsonOut), fmt.Sprintf("werror=%v", werror))
	return cache.New(root, 0), h.Key(), true
}

// headerFiles lists the in-package C and H files that enter the cache key.
func headerFiles(root string) []string {
	var out []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "build" || name == "desc") {
				return fs.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); ext == ".c" || ext == ".h" {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
