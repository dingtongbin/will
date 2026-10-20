package pkg

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/cimport"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/toolchain"
)

// applyCHeaders fills the prototype of every import c link from its header.
// An import without an explicit link block takes every function the subset can
// read. A header that cannot be found is an error only when the import would
// otherwise have no names; an explicit link list keeps the old untyped
// fallback so a local header is not required for a hand-written binding.
func applyCHeaders(f *ast.File, dir, root string) []diag.Diagnostic {
	var bag diag.Bag
	for _, imp := range f.Imports {
		if !imp.C {
			continue
		}
		path, ok := findCHeaderPath(imp.Path, dir, root)
		if !ok {
			if len(imp.Links) == 0 {
				bag.Error(imp.Span.Start, diag.CodeCannotParseCHeader,
					"cannot parse "+imp.Path,
					"set WILL_C_INCLUDE to a directory that holds the header, or list the links explicitly")
			}
			continue
		}
		var funcs []cimport.Func
		var err error
		if clang, findErr := toolchain.FindClang(); findErr == nil {
			funcs, err = cimport.ParseClang(clang.Path, path, CIncludeDirs(dir, root), "")
		} else {
			// A machine without clang can still check an explicitly listed
			// link; the small fallback reads the local declaration subset.
			var src []byte
			src, err = os.ReadFile(path)
			if err == nil {
				funcs, err = cimport.Parse(imp.Path, src)
			}
		}
		if err != nil {
			bag.Error(imp.Span.Start, diag.CodeCannotParseCHeader,
				"cannot parse "+imp.Path, err.Error())
			continue
		}
		byName := map[string]cimport.Func{}
		for _, fn := range funcs {
			byName[fn.Name] = fn
		}
		if len(imp.Links) == 0 {
			names := make([]string, 0, len(byName))
			for name := range byName {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				fn := byName[name]
				imp.Links = append(imp.Links, &ast.Link{
					Span:     imp.Span,
					Name:     fn.Name,
					Typed:    true,
					Params:   fn.Params,
					Result:   fn.Result,
					Variadic: fn.Variadic,
				})
			}
			continue
		}
		for _, link := range imp.Links {
			fn, ok := byName[link.Name]
			if !ok {
				continue
			}
			link.Typed = true
			link.Params = fn.Params
			link.Result = fn.Result
			link.Variadic = fn.Variadic
		}
	}
	return bag.Diagnostics()
}

// ReadCHeader is the cache-facing form of the header search. It returns the
// bytes the compiler will parse, or false when no search directory has it.
func ReadCHeader(name, dir, root string) ([]byte, bool) {
	return readCHeader(name, dir, root)
}

// FindCHeader returns the resolved path of one header, using the same search
// order as ReadCHeader.
func FindCHeader(name, dir, root string) (string, bool) {
	return findCHeaderPath(name, dir, root)
}

// CIncludeDirs returns the header search directories in lookup order.
func CIncludeDirs(dir, root string) []string {
	var dirs []string
	dirs = append(dirs, dir, root, filepath.Join(root, "include"))
	for _, env := range []string{"WILL_C_INCLUDE", "CPATH", "C_INCLUDE_PATH"} {
		for _, part := range filepath.SplitList(os.Getenv(env)) {
			if part != "" {
				dirs = append(dirs, part)
			}
		}
	}
	dirs = append(dirs, "/usr/local/include", "/usr/include")
	return dirs
}

// readCHeader tries the package directory, the module root, the explicit
// include path, and the host's usual include directories, in that order.
func readCHeader(name, dir, root string) ([]byte, bool) {
	path, ok := findCHeaderPath(name, dir, root)
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(path)
	return data, err == nil
}

func findCHeaderPath(name, dir, root string) (string, bool) {
	if name == "" {
		return "", false
	}
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err == nil {
			return name, true
		}
		return "", false
	}
	for _, base := range CIncludeDirs(dir, root) {
		path := filepath.Join(base, filepath.FromSlash(name))
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

// cHeaderPath is a small helper for tests: it joins a slash-separated header
// path without depending on filepath in the caller.
func cHeaderPath(dir, name string) string {
	return filepath.Join(dir, strings.TrimPrefix(filepath.FromSlash(name), string(filepath.Separator)))
}
