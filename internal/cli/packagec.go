package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/pkg"
	"github.com/will-lang/will/internal/toolchain"
)

// packageCObjects compiles the C sources of the built module into a
// content-addressed cache. A source or header edit changes the key, so a stale
// object cannot be reused.
func packageCObjects(opts buildOptions, b *pkg.Build) ([]string, func(), error) {
	noop := func() {}
	var sources, headers []string
	seen := map[string]bool{}
	for _, p := range b.Packages {
		err := filepath.Walk(p.Dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			switch filepath.Ext(path) {
			case ".c":
				if !seen[path] {
					seen[path] = true
					sources = append(sources, path)
				}
			case ".h":
				if !seen[path] {
					seen[path] = true
					headers = append(headers, path)
				}
			}
			return nil
		})
		if err != nil {
			return nil, noop, err
		}
	}
	if len(sources) == 0 {
		return nil, noop, nil
	}
	sort.Strings(sources)
	sort.Strings(headers)
	clang, err := toolchain.FindClang()
	if err != nil {
		return nil, noop, err
	}
	root, err := cache.Root()
	if err != nil {
		return nil, noop, err
	}
	limit, _ := cache.Limit()
	h := cache.NewHasher()
	version, _ := exec.Command(clang.Path, "--version").Output()
	h.AddStrings("package-c", clang.Path, string(version), targetName(opts.target),
		opts.opt, fmt.Sprintf("debug=%v", opts.debug), fmt.Sprintf("lib=%v", opts.lib),
		fmt.Sprintf("lto=%v", opts.lto), "include="+os.Getenv("WILL_C_INCLUDE"))
	for _, path := range sources {
		if err := hashFile(h, b.Root, path, "source"); err != nil {
			return nil, noop, err
		}
	}
	for _, path := range headers {
		if err := hashFile(h, b.Root, path, "header"); err != nil {
			return nil, noop, err
		}
	}
	if runtime, err := runtimeRoot(); err == nil {
		if err := hashHeaderDir(h, runtime, filepath.Join(runtime, "include"), "runtime-header"); err != nil {
			return nil, noop, err
		}
	}
	for _, include := range filepath.SplitList(os.Getenv("WILL_C_INCLUDE")) {
		if include == "" {
			continue
		}
		if err := hashHeaderDir(h, include, include, "external-header"); err != nil {
			return nil, noop, err
		}
	}
	dir := filepath.Join(root, "pkgc", h.Key())
	ext := ".o"
	if opts.lto {
		ext = ".bc"
	}
	objects := make([]string, len(sources))
	for i := range sources {
		objects[i] = filepath.Join(dir, fmt.Sprintf("c%d%s", i, ext))
	}
	if allFiles(objects) {
		return objects, noop, nil
	}
	base := filepath.Dir(dir)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, noop, err
	}
	temporary, err := os.MkdirTemp(base, filepath.Base(dir)+"-")
	if err != nil {
		return nil, noop, err
	}
	defer os.RemoveAll(temporary)
	for i, source := range sources {
		emit := []string{"-c"}
		if opts.lto {
			emit = []string{"-emit-llvm", "-c"}
		}
		args := append([]string{"-std=c11"}, emit...)
		args = append(args, source, "-o", filepath.Join(temporary, filepath.Base(objects[i])))
		args = append(args, "-I", filepath.Dir(source))
		if runtime, err := runtimeRoot(); err == nil {
			args = append(args, "-I", filepath.Join(runtime, "include"))
		}
		for _, include := range filepath.SplitList(os.Getenv("WILL_C_INCLUDE")) {
			if include != "" {
				args = append(args, "-I", include)
			}
		}
		if opts.opt != "" {
			args = append(args, opts.opt)
		}
		if opts.debug {
			args = append(args, "-g")
		}
		if opts.lib {
			args = append(args, "-fPIC")
		}
		if opts.target != "" {
			args = append(args, "--target="+opts.target)
		}
		cmd := exec.Command(clang.Path, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return nil, noop, fmt.Errorf("%s: %v\n%s", source, err, output)
		}
	}
	if err := os.Rename(temporary, dir); err != nil {
		if !allFiles(objects) {
			return nil, noop, err
		}
	}
	store := cache.New(filepath.Join(root, "pkgc"), limit)
	_ = store.Trim()
	return objects, noop, nil
}

// hashHeaderDir hashes every .h under one include directory.
func hashHeaderDir(h *cache.Hasher, root, dir, kind string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".h" {
			return nil
		}
		return hashFile(h, root, path, kind)
	})
}

func hashFile(h *cache.Hasher, root, path, kind string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	h.Add(kind, []byte(filepath.ToSlash(rel)))
	h.Add(kind, data)
	return nil
}

func allFiles(paths []string) bool {
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}
