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

	units, code := sourceUnits(paths[0], stdout, stderr)
	if code != 0 {
		return code
	}
	records, err := decisionRecords(units, target)
	if err != nil {
		reportTranslate(stdout, &willc.Error{Message: err.Error()})
		return 1
	}
	return writeText(outPath, decisions.Write(records), stdout, stderr)
}

// decisionRecords builds the decision record of one set of files: the layout of
// every named type and one index obligation per container loop.
func decisionRecords(units []sourceUnit, target string) ([]decisions.Record, error) {
	res := layout.New(layout.Target{Word: wordOf(target)})
	for _, u := range units {
		if err := registerTypes(res, u); err != nil {
			return nil, err
		}
	}
	var records []decisions.Record
	for _, name := range res.Names() {
		got, err := res.Layout(name)
		if err != nil {
			return nil, err
		}
		records = append(records, decisions.Layout(got.Name, got.Type.Size, got.Type.Align))
		for _, f := range got.Fields {
			records = append(records, decisions.FieldOffset(got.Name, f.Name, f.Offset, f.Size))
		}
	}
	for _, u := range units {
		collectObligations(u.file, &records)
	}
	return records, nil
}

// sourceUnit is one file with the package path its types are named under.
type sourceUnit struct {
	file   *ast.File
	prefix string
}

// qualifiedName puts a package path in front of a type name. A standalone file
// has no package path, so its types keep the bare name.
func qualifiedName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// registerTypes adds the structs and enums of one file to the resolver. Each
// type is registered under its package-qualified name, so two packages may
// declare the same type name without colliding.
func registerTypes(res *layout.Resolver, u sourceUnit) error {
	f := u.file
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.StructDecl:
			// A generic struct has one layout per instantiation, so it has no
			// single decision record.
			if len(decl.TypeParams) > 0 {
				continue
			}
			fields := make([]layout.Field, len(decl.Fields))
			for i, fd := range decl.Fields {
				spec, err := layoutSpec(fd.Type, u.prefix)
				if err != nil {
					return fmt.Errorf("field %s of type %s: %w", fd.Name, decl.Name, err)
				}
				fields[i] = layout.Field{Name: fd.Name, Type: spec}
			}
			if err := res.AddStruct(qualifiedName(u.prefix, decl.Name), fields); err != nil {
				return err
			}
		case *ast.EnumDecl:
			if len(decl.TypeParams) > 0 {
				continue
			}
			variants := make([]layout.Variant, len(decl.Variants))
			for i, v := range decl.Variants {
				args := make([]*layout.Spec, len(v.Args))
				for j, a := range v.Args {
					spec, err := layoutSpec(a, u.prefix)
					if err != nil {
						return fmt.Errorf("variant %s of type %s: %w", v.Name, decl.Name, err)
					}
					args[j] = spec
				}
				variants[i] = layout.Variant{Name: v.Name, Args: args}
			}
			if err := res.AddEnum(qualifiedName(u.prefix, decl.Name), variants); err != nil {
				return err
			}
		case *ast.InterfaceDecl:
			if len(decl.TypeParams) > 0 {
				continue
			}
			if err := res.AddInterface(qualifiedName(u.prefix, decl.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// layoutSpec turns a parsed type into the specification the layout package
// lays out. A length that is not an integer literal is reported because the
// layout is fixed at compile time. A bare named type belongs to the current
// package, so it is qualified with that package path; a name that already
// carries a dot is an imported type and is left for a later pass.
// predeclaredLayoutType reports a generic type name the layout resolver knows
// without a declaration in the package.
func predeclaredLayoutType(name string) bool {
	switch name {
	case "Array", "Map", "Set", "chan", "Task", "rc", "weak", "Option", "Result", "Range":
		return true
	}
	return false
}

func layoutSpec(t *ast.Type, prefix string) (*layout.Spec, error) {
	if t == nil {
		return nil, fmt.Errorf("the type is missing")
	}
	switch t.Kind {
	case ast.ScalarType:
		return &layout.Spec{Kind: layout.ScalarKind, Name: t.Name}, nil
	case ast.NamedType:
		name := t.Name
		if !strings.Contains(name, ".") && name != "Error" && !predeclaredLayoutType(name) {
			name = qualifiedName(prefix, name)
		}
		if len(t.Args) == 0 {
			return &layout.Spec{Kind: layout.NamedKind, Name: name}, nil
		}
		args := make([]*layout.Spec, len(t.Args))
		for i, a := range t.Args {
			spec, err := layoutSpec(a, prefix)
			if err != nil {
				return nil, err
			}
			args[i] = spec
		}
		return &layout.Spec{Kind: layout.GenericKind, Name: name, Args: args}, nil
	case ast.ArrayType:
		n, err := strconv.Atoi(t.Len)
		if err != nil {
			return nil, fmt.Errorf("the array length %q is not an integer literal", t.Len)
		}
		elem, err := layoutSpec(t.Elem, prefix)
		if err != nil {
			return nil, err
		}
		return &layout.Spec{Kind: layout.ArrayKind, Len: n, Elem: elem}, nil
	case ast.ViewType:
		elem, err := layoutSpec(t.Elem, prefix)
		if err != nil {
			return nil, err
		}
		return &layout.Spec{Kind: layout.SliceKind, Elem: elem}, nil
	case ast.FuncType:
		return &layout.Spec{Kind: layout.FuncKind}, nil
	case ast.UnitType:
		return &layout.Spec{Kind: layout.UnitKind}, nil
	}
	return nil, fmt.Errorf("the layout of %s is not known", ast.TypeText(t))
}

// sourceUnits lists the files whose declarations enter the record, in package
// path order and then file order, with the package path of each.
func sourceUnits(path string, stdout, stderr io.Writer) ([]sourceUnit, int) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, 1
	}
	if info.IsDir() {
		b := pkg.Load(path)
		if b.Err != nil {
			return nil, loadFailure(b.Err, stderr)
		}
		if hasError(b.Diags) {
			io.WriteString(stdout, diag.FormatAll(b.Diags))
			return nil, 1
		}
		var out []sourceUnit
		for _, p := range b.Packages {
			for _, f := range p.Files {
				if f.AST != nil {
					out = append(out, sourceUnit{file: f.AST, prefix: p.Path})
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
	prefix := ""
	if b, p := modulePackage(path); b != nil {
		prefix = p.Path
	}
	return []sourceUnit{{file: f, prefix: prefix}}, 0
}
