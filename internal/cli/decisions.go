package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/decisions"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/layout"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/pkg"
	"github.com/will-lang/will/internal/willc"
)

// runEmitDecisions writes the decision record of one module or one file. The
// record holds the decisions this build made; today those are the layout of
// every struct and the place of each of its fields.
func runEmitDecisions(args []string, stdout, stderr io.Writer) int {
	outPath := ""
	var paths []string
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
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(stderr, "will: unknown flag %s\n", args[i])
				return 2
			}
			paths = append(paths, args[i])
		}
	}
	if len(paths) != 1 {
		fmt.Fprint(stderr, "usage: will emit-decisions [--out <path>] <dir or file>\n")
		return 2
	}

	files, code := sourceFiles(paths[0], stderr)
	if code != 0 {
		return code
	}
	var records []decisions.Record
	for _, f := range files {
		for _, d := range f.Decls {
			decl, ok := d.(*ast.StructDecl)
			if !ok {
				continue
			}
			fields := make([]layout.Field, len(decl.Fields))
			for i, fd := range decl.Fields {
				fields[i] = layout.Field{Name: fd.Name, Type: ast.TypeText(fd.Type)}
			}
			got, err := layout.Of(decl.Name, fields)
			if err != nil {
				reportTranslate(stdout, &willc.Error{Message: err.Error()})
				return 1
			}
			records = append(records, decisions.Layout(got.Name, got.Size, got.Align))
			for i, fd := range got.Fields {
				size, _, _ := layout.Scalar(fd.Type)
				records = append(records, decisions.FieldOffset(got.Name, fd.Name, got.Offsets[i], size))
			}
		}
	}
	return writeText(outPath, decisions.Write(records), stdout, stderr)
}

// sourceFiles lists the files whose declarations enter the record, in package
// path order and then file order.
func sourceFiles(path string, stderr io.Writer) ([]*ast.File, int) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, 1
	}
	if info.IsDir() {
		b := pkg.Load(path)
		if b.Err != nil {
			fmt.Fprintf(stderr, "will: %v\n", b.Err)
			return nil, 1
		}
		var out []*ast.File
		for _, p := range b.Packages {
			for _, f := range p.Files {
				if f.AST != nil {
					out = append(out, f.AST)
				}
			}
		}
		return out, 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, 1
	}
	f, ds := parser.Parse(filepath.ToSlash(path), data)
	if len(ds) != 0 {
		io.WriteString(stderr, diag.FormatAll(ds))
		return nil, 1
	}
	return []*ast.File{f}, 0
}

// The layout package reports a field's size directly.
