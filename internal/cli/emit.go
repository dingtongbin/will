package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/check"
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
		name, inline, hasInline := splitFlag(args[i])
		switch name {
		case "--out":
			if hasInline {
				outPath = inline
				break
			}
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "will: --out needs a path\n")
				return 2
			}
			i++
			outPath = args[i]
		case "--module":
			if hasInline {
				modulePath = inline
				break
			}
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
		fmt.Fprint(stderr, "usage: will emit-willc [--out=<path>] [--module <path>] <file>\n")
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

	imps, defModule, names, others := packageContext(file)
	if modulePath == "" {
		modulePath = defModule
	}
	res, rds := resolve.ResolveIn(f, names, imps)
	bag.AddAll(rds)
	bag.AddAll(types.CheckWith(f, res, others))
	if !bag.HasErrors() {
		bag.AddAll(check.Moves(f))
	}

	all := bag.Diagnostics()
	if hasError(all) {
		io.WriteString(stdout, diag.FormatAll(all))
		return 1
	}
	text, err := willc.Emit(f, res, modulePath, all)
	if err != nil {
		reportTranslate(stdout, err)
		return 1
	}
	return writeText(outPath, text, stdout, stderr)
}

// packageContext loads the module that contains file and returns the import
// tables, the package path of the file's own package, that package's
// top-level names, and the other files of the package.
func packageContext(file string) (resolve.Imports, string, resolve.Exported, []*ast.File) {
	absFile, err := filepath.Abs(file)
	if err != nil {
		absFile = file
	}
	dir := filepath.Dir(absFile)
	b := pkg.Load(dir)
	if b.Err != nil || b.Mod == nil {
		return nil, filepath.Base(dir), nil, nil
	}
	imps := resolve.Imports{}
	for _, p := range b.Packages {
		imps[p.Path] = p.Exports
	}
	def := b.Mod.Path
	var names resolve.Exported
	var others []*ast.File
	for _, p := range b.Packages {
		if filepath.Clean(p.Dir) != filepath.Clean(dir) {
			continue
		}
		def = p.Path
		names = p.Names
		for _, f := range p.Files {
			if f.AST == nil || filepath.Clean(filepath.Join(p.Dir, f.Path)) == filepath.Clean(absFile) {
				continue
			}
			others = append(others, f.AST)
		}
	}
	return imps, def, names, others
}

func hasError(ds []diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}
