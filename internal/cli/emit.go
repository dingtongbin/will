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

	imps, defModule, names, others, aliases := packageContext(file)
	if modulePath == "" {
		modulePath = defModule
	}
	res, rds := resolve.ResolveIn(f, names, imps)
	bag.AddAll(rds)
	checked, tds := types.CheckWithFull(f, res, others)
	bag.AddAll(tds)
	if !bag.HasErrors() {
		bag.AddAll(check.Moves(f))
	}

	all := bag.Diagnostics()
	if hasError(all) {
		io.WriteString(stdout, diag.FormatAll(all))
		return 1
	}
	text, err := willc.Emit(f, res, modulePath, all, checked.LiteralTypes, aliases)
	if err != nil {
		reportTranslate(stdout, err)
		return 1
	}
	return writeText(outPath, text, stdout, stderr)
}

// findModuleRoot walks up from dir to the nearest directory with a will.mod.
// A file may sit in a subdirectory of its module (src/main.will), so looking
// only at the file's own directory would lose the module and every import.
func findModuleRoot(dir string) (string, bool) {
	for {
		info, err := os.Stat(filepath.Join(dir, "will.mod"))
		if err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// packageContext loads the module that contains file and returns the import
// tables, the package path of the file's own package, that package's top-level
// names, the other files of the package, and the alias map of the module.
func packageContext(file string) (resolve.Imports, string, resolve.Exported, []*ast.File, map[string]string) {
	absFile, err := filepath.Abs(file)
	if err != nil {
		absFile = file
	}
	dir := filepath.Dir(absFile)
	root, inside := findModuleRoot(dir)
	if !inside {
		return nil, filepath.Base(dir), nil, nil, nil
	}
	b := pkg.Load(root)
	if b.Err != nil || b.Mod == nil {
		return nil, filepath.Base(dir), nil, nil, nil
	}
	imps := b.Imports()
	aliases := b.ImportAliases()
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
	return imps, def, names, others, aliases
}

func hasError(ds []diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}
