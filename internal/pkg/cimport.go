package pkg

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/cimport"
	"github.com/will-lang/will/internal/diag"
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
		src, ok := readCHeader(imp.Path, dir, root)
		if !ok {
			if len(imp.Links) == 0 {
				bag.Error(imp.Span.Start, diag.CodeCannotParseCHeader,
					"cannot parse "+imp.Path,
					"set WILL_C_INCLUDE to a directory that holds the header, or list the links explicitly")
			}
			continue
		}
		funcs, err := cimport.Parse(imp.Path, src)
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

// readCHeader tries the package directory, the module root, the explicit
// include path, and the host's usual include directories, in that order.
func readCHeader(name, dir, root string) ([]byte, bool) {
	if name == "" {
		return nil, false
	}
	if filepath.IsAbs(name) {
		data, err := os.ReadFile(name)
		return data, err == nil
	}
	var dirs []string
	dirs = append(dirs, dir, root)
	for _, env := range []string{"WILL_C_INCLUDE", "CPATH", "C_INCLUDE_PATH"} {
		for _, part := range filepath.SplitList(os.Getenv(env)) {
			if part != "" {
				dirs = append(dirs, part)
			}
		}
	}
	dirs = append(dirs, "/usr/local/include", "/usr/include")
	for _, base := range dirs {
		data, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(name)))
		if err == nil {
			return data, true
		}
	}
	return nil, false
}

// cHeaderPath is a small helper for tests: it joins a slash-separated header
// path without depending on filepath in the caller.
func cHeaderPath(dir, name string) string {
	return filepath.Join(dir, strings.TrimPrefix(filepath.FromSlash(name), string(filepath.Separator)))
}
