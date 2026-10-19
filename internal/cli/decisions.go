package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
// every named type and the place of each field or variant.
func runEmitDecisions(args []string, stdout, stderr io.Writer) int {
	outPath := ""
	target := ""
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
		case "--target":
			if hasInline {
				target = inline
				break
			}
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "will: --target needs a triple\n")
				return 2
			}
			i++
			target = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(stderr, "will: unknown flag %s\n", args[i])
				return 2
			}
			paths = append(paths, args[i])
		}
	}
	if len(paths) != 1 {
		fmt.Fprint(stderr, "usage: will emit-decisions [--out <path>] [--target <triple>] <dir or file>\n")
		return 2
	}
	if d, bad := targetDiagnostic(target, target != ""); bad {
		reportNoPos(d, false, diag.Build{}, stdout)
		return 1
	}

	files, code := sourceFiles(paths[0], stdout, stderr)
	if code != 0 {
		return code
	}
	res := layout.New(layout.Target{Word: wordOf(target)})
	for _, f := range files {
		if err := registerTypes(res, f); err != nil {
			reportTranslate(stdout, &willc.Error{Message: err.Error()})
			return 1
		}
	}
	var records []decisions.Record
	for _, name := range res.Names() {
		got, err := res.Layout(name)
		if err != nil {
			reportTranslate(stdout, &willc.Error{Message: err.Error()})
			return 1
		}
		records = append(records, decisions.Layout(got.Name, got.Type.Size, got.Type.Align))
		for _, f := range got.Fields {
			records = append(records, decisions.FieldOffset(got.Name, f.Name, f.Offset, f.Size))
		}
	}
	return writeText(outPath, decisions.Write(records), stdout, stderr)
}

// registerTypes adds the structs and enums of one file to the resolver.
func registerTypes(res *layout.Resolver, f *ast.File) error {
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.StructDecl:
			fields := make([]layout.Field, len(decl.Fields))
			for i, fd := range decl.Fields {
				spec, err := layoutSpec(fd.Type)
				if err != nil {
					return fmt.Errorf("field %s of type %s: %w", fd.Name, decl.Name, err)
				}
				fields[i] = layout.Field{Name: fd.Name, Type: spec}
			}
			if err := res.AddStruct(decl.Name, fields); err != nil {
				return err
			}
		case *ast.EnumDecl:
			variants := make([]layout.Variant, len(decl.Variants))
			for i, v := range decl.Variants {
				args := make([]*layout.Spec, len(v.Args))
				for j, a := range v.Args {
					spec, err := layoutSpec(a)
					if err != nil {
						return fmt.Errorf("variant %s of type %s: %w", v.Name, decl.Name, err)
					}
					args[j] = spec
				}
				variants[i] = layout.Variant{Name: v.Name, Args: args}
			}
			if err := res.AddEnum(decl.Name, variants); err != nil {
				return err
			}
		}
	}
	return nil
}

// layoutSpec turns a parsed type into the specification the layout package
// lays out. A length that is not an integer literal is reported because the
// layout is fixed at compile time.
func layoutSpec(t *ast.Type) (*layout.Spec, error) {
	if t == nil {
		return nil, fmt.Errorf("the type is missing")
	}
	switch t.Kind {
	case ast.ScalarType:
		return &layout.Spec{Kind: layout.ScalarKind, Name: t.Name}, nil
	case ast.NamedType:
		if len(t.Args) == 0 {
			return &layout.Spec{Kind: layout.NamedKind, Name: t.Name}, nil
		}
		args := make([]*layout.Spec, len(t.Args))
		for i, a := range t.Args {
			spec, err := layoutSpec(a)
			if err != nil {
				return nil, err
			}
			args[i] = spec
		}
		return &layout.Spec{Kind: layout.GenericKind, Name: t.Name, Args: args}, nil
	case ast.ArrayType:
		n, err := strconv.Atoi(t.Len)
		if err != nil {
			return nil, fmt.Errorf("the array length %q is not an integer literal", t.Len)
		}
		elem, err := layoutSpec(t.Elem)
		if err != nil {
			return nil, err
		}
		return &layout.Spec{Kind: layout.ArrayKind, Len: n, Elem: elem}, nil
	case ast.ViewType:
		elem, err := layoutSpec(t.Elem)
		if err != nil {
			return nil, err
		}
		return &layout.Spec{Kind: layout.SliceKind, Elem: elem}, nil
	case ast.UnitType:
		return &layout.Spec{Kind: layout.UnitKind}, nil
	}
	return nil, fmt.Errorf("the layout of %s is not known", ast.TypeText(t))
}

// sourceFiles lists the files whose declarations enter the record, in package
// path order and then file order.
func sourceFiles(path string, stdout, stderr io.Writer) ([]*ast.File, int) {
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
		if hasError(b.Diags) {
			io.WriteString(stdout, diag.FormatAll(b.Diags))
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
		io.WriteString(stdout, diag.FormatAll(ds))
		return nil, 1
	}
	return []*ast.File{f}, 0
}
