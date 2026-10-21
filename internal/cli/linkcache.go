package cli

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/toolchain"
)

// linkCacheKey hashes the LLVM input, the complete clang argument list, clang
// itself, and every input file clang reads. The argument list covers the target,
// optimization level, debug/lib mode, runtime bitcode, and package C objects;
// file contents cover a runtime or package C edit even when its path is stable.
func linkCacheKey(ir []byte, clang *toolchain.Clang, args []string) (*cache.Store, string) {
	root, err := cache.Root()
	if err != nil {
		return nil, ""
	}
	limit, _ := cache.Limit()
	h := cache.NewHasher()
	h.Add("ir", ir)
	h.AddStrings("clang", clang.Path, clangVersion(clang))
	for _, arg := range args {
		if info, err := os.Stat(arg); err == nil && !info.IsDir() {
			// Package C inputs live in a fresh temporary directory each
			// build. Hash the base name and bytes, not that random path.
			h.AddStrings("file", filepath.Base(arg))
			if data, err := os.ReadFile(arg); err == nil {
				h.Add("input", data)
			}
			continue
		}
		h.AddStrings("arg", arg)
	}
	return cache.New(filepath.Join(root, "link"), limit), h.Key()
}

// clangVersion identifies the exact compiler used by the link cache.
func clangVersion(clang *toolchain.Clang) string {
	out, err := exec.Command(clang.Path, "--version").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// cachedLink copies a previous executable or library to out and reports hit.
func cachedLink(store *cache.Store, key, out string) bool {
	if store == nil || key == "" {
		return false
	}
	got, ok := store.Get(key, cache.Artifact)
	if !ok {
		return false
	}
	if err := os.WriteFile(out, got[cache.Artifact], 0o755); err != nil {
		return false
	}
	return true
}

// rememberLink stores the linked output for the next build with the same key.
func rememberLink(store *cache.Store, key, out string) {
	if store == nil || key == "" {
		return
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return
	}
	if err := store.Put(key, map[string][]byte{cache.Artifact: data}); err == nil {
		_ = store.TrimIfNeeded()
	}
}
