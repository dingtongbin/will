package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/pkg"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/types"
	"github.com/will-lang/will/internal/willc"
)

// runEmitWillc writes the willc text of one file.
func runEmitWillc(args []string, stdout, stderr io.Writer) int {
	outPath := ""
	modulePath := ""
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "will: --out needs a path\n")
				return 2
			}
			i++
			outPath = args[i]
		case "--module":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "will: --module needs a path\n")
				return 2
			}
			i++
			modulePath = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(stderr, "will: unknown flag %s\n", args[i])
				return 2
			}
			files = append(files, args[i])
		}
	}
	if len(files) != 1 {
		fmt.Fprint(stderr, "usage: will emit-willc [--out <path>] [--module <path>] <file>\n")
		return 2
	}

	file := files[0]
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	f, ds := parser.Parse(filepath.ToSlash(file), data)
	if f == nil {
		io.WriteString(stdout, diag.FormatAll(ds))
		return 1
	}
	var bag diag.Bag
	bag.AddAll(ds)

	imps, defModule := packageImports(file)
	if modulePath == "" {
		modulePath = defModule
	}
	res, rds := resolve.ResolveWith(f, imps)
	bag.AddAll(rds)
	bag.AddAll(types.Check(f, res))

	all := bag.Diagnostics()
	if hasError(all) {
		io.WriteString(stdout, diag.FormatAll(all))
		return 1
	}
	text, err := willc.Emit(f, res, modulePath, all)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	if outPath == "" {
		stdout.Write(text)
		return 0
	}
	if err := os.WriteFile(outPath, text, 0o644); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	return 0
}

// packageImports loads the module that contains file, when there is one, and
// returns the import tables plus the module path of the file's package.
func packageImports(file string) (resolve.Imports, string) {
	dir := filepath.Dir(file)
	b := pkg.Load(dir)
	if b.Err != nil || b.Mod == nil {
		return nil, filepath.Base(dir)
	}
	imps := resolve.Imports{}
	for _, p := range b.Packages {
		imps[p.Path] = p.Exports
	}
	def := b.Mod.Path
	for _, p := range b.Packages {
		if filepath.Clean(p.Dir) == filepath.Clean(dir) {
			def = p.Path
		}
	}
	return imps, def
}

func hasError(ds []diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}
