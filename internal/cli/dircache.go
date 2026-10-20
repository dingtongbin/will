package cli

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/pkg"
)

// runCachedBuild checks a loaded module, using the directory key when the
// cache is on.
func runCachedBuild(b *pkg.Build, flags checkFlags, stdout io.Writer) int {
	store, key, cacheable := dirCacheKey(b, flags)
	if cacheable {
		if data, hit := store.Get(key, cache.Diag); hit {
			out, code := decodeDiag(data[cache.Diag])
			stdout.Write(out)
			return code
		}
	}
	ds := b.CheckAll()
	if flags.freestanding {
		ds = append(ds, b.Freestanding()...)
	}
	ds = diag.FilterDisabled(ds, flags.disabled)
	build := buildIdentityWith(flags.opt, flags.target)
	var out []byte
	switch {
	case flags.json:
		out = []byte(diag.JSONAll(ds, build))
	case flags.long:
		out = []byte(diag.FormatAllLong(ds, sourceReader()))
	default:
		out = []byte(diag.FormatAll(ds))
	}
	stdout.Write(out)
	code := exitCode(ds, flags.werror)
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
func dirCacheKey(b *pkg.Build, flags checkFlags) (*cache.Store, string, bool) {
	root, err := cache.Root()
	if err != nil || b.Mod == nil {
		return nil, "", false
	}
	limit, _ := cache.Limit()
	h := cache.NewHasher()
	// A module whose directory name is invalid still reports B0001 before any
	// package exists, so the root's own name and path must be in the key even
	// when the package list is empty.
	h.AddStrings("root-dir", filepath.Base(b.Root))
	h.AddStrings("root-module", b.Mod.Path)
	// Every module of the graph contributes its manifest, its sum file, and its
	// C and H files. The paths are relative to each module root and prefixed by
	// the module path, so moving the checkout does not change the key.
	roots := map[string]string{}
	for _, p := range b.Packages {
		if p.Root == "" {
			continue
		}
		if cur, ok := roots[p.Root]; !ok || p.Path < cur {
			roots[p.Root] = p.Path
		}
	}
	prefixes := make([]string, 0, len(roots))
	byPrefix := map[string]string{}
	for root, prefix := range roots {
		prefixes = append(prefixes, prefix)
		byPrefix[prefix] = root
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		root := byPrefix[prefix]
		h.AddStrings("module", prefix)
		// The directory's base name is part of B0001, and two checkouts of the
		// same module may sit in different directories. Keep the name, not the
		// absolute path, so a moved checkout still hits.
		h.AddStrings("module-dir", filepath.Base(root))
		if data, err := os.ReadFile(filepath.Join(root, "will.mod")); err == nil {
			h.Add(prefix+"/will.mod", data)
		}
		if data, err := os.ReadFile(filepath.Join(root, "will.sum")); err == nil {
			h.Add(prefix+"/will.sum", data)
		}
		for _, path := range headerFiles(root) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				continue
			}
			if data, err := os.ReadFile(path); err == nil {
				h.Add(prefix+"/"+filepath.ToSlash(rel), data)
			}
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
			if data, err := os.ReadFile(filepath.Join(p.Root, f.Path)); err == nil {
				h.Add(p.Path+"/"+f.Path, data)
			}
		}
	}
	build := buildIdentityWith(flags.opt, flags.target)
	h.AddStrings("compiler", compilerIdentity(), build.Target, build.Opt)
	h.AddStrings("flags", fmt.Sprintf("json=%v", flags.json), fmt.Sprintf("werror=%v", flags.werror),
		"features="+flags.features, fmt.Sprintf("freestanding=%v", flags.freestanding),
		"disabled="+disabledList(flags.disabled))
	return cache.New(root, limit), h.Key(), true
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
