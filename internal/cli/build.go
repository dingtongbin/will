package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/gen"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/pkg"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/toolchain"
	"github.com/will-lang/will/internal/types"
	"github.com/will-lang/will/internal/willc"
)

// defaultEntry is the entry function name. The language volume does not fix
// an entry point yet, so this is provisional.
const defaultEntry = "main"

// buildOptions is one build invocation.
type buildOptions struct {
	path   string
	entry  string
	out    string
	emitIR bool
}

func compileIR(path, entry string, stderr io.Writer) ([]byte, string, int) {
	file, modulePath, code := entryFile(path, entry, stderr)
	if code != 0 {
		return nil, "", code
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, "", 1
	}
	f, ds := parser.Parse(filepath.ToSlash(file), data)
	var bag diag.Bag
	bag.AddAll(ds)
	if f == nil {
		fmt.Fprint(stderr, diag.FormatAll(bag.Diagnostics()))
		return nil, "", 1
	}
	if !fileDeclaresEntry(f, entry) {
		fmt.Fprintf(stderr, "will: entry function %s not found\n", entry)
		return nil, "", 1
	}
	res, rds := resolve.Resolve(f)
	bag.AddAll(rds)
	bag.AddAll(types.Check(f, res))
	all := bag.Diagnostics()
	if hasError(all) {
		fmt.Fprint(stderr, diag.FormatAll(all))
		return nil, "", 1
	}
	text, err := willc.Emit(f, res, modulePath, all)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, "", 1
	}
	doc, err := willc.Read(text)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, "", 1
	}
	ir, err := gen.Emit(doc)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, "", 1
	}
	return ir, modulePath, 0
}

// entryFile finds the source file that declares the entry function and its
// module path.
func entryFile(path, entry string, stderr io.Writer) (string, string, int) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return "", "", 1
	}
	if !info.IsDir() {
		return path, filepath.Base(filepath.Dir(path)), 0
	}
	b := pkg.Load(path)
	if b.Err != nil {
		fmt.Fprintf(stderr, "will: %v\n", b.Err)
		return "", "", 1
	}
	for _, p := range b.Packages {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, d := range f.AST.Decls {
				if declaresEntry(d, entry) {
					return filepath.Join(p.Dir, f.Path), p.Path, 0
				}
			}
		}
	}
	fmt.Fprintf(stderr, "will: entry function %s not found\n", entry)
	return "", "", 1
}

func declaresEntry(d ast.Decl, entry string) bool {
	fn, ok := d.(*ast.FuncDecl)
	return ok && fn.Recv == nil && fn.Name == entry
}

func fileDeclaresEntry(f *ast.File, entry string) bool {
	for _, d := range f.Decls {
		if declaresEntry(d, entry) {
			return true
		}
	}
	return false
}

func runBuild(args []string, stdout, stderr io.Writer) int {
	opts, code := parseBuildArgs(args, stderr)
	if code != 0 {
		return code
	}
	ir, _, code := compileIR(opts.path, opts.entry, stderr)
	if code != 0 {
		return code
	}
	if opts.emitIR {
		stdout.Write(ir)
		return 0
	}
	clang, err := toolchain.FindClang()
	if err != nil {
		return missingTool(stdout, "clang")
	}
	out := opts.out
	if out == "" {
		out = defaultOutput(opts.path)
	}
	output, err := clang.BuildIR(ir, out)
	if err != nil {
		fmt.Fprintf(stdout, "%s", output)
		fmt.Fprintf(stderr, "will: link failed\n")
		return 1
	}
	return 0
}

func runRun(args []string, stdout, stderr io.Writer) int {
	opts, code := parseBuildArgs(args, stderr)
	if code != 0 {
		return code
	}
	ir, _, code := compileIR(opts.path, opts.entry, stderr)
	if code != 0 {
		return code
	}
	clang, err := toolchain.FindClang()
	if err != nil {
		return missingTool(stdout, "clang")
	}
	out := opts.out
	if out == "" {
		dir, err := os.MkdirTemp("", "will-run")
		if err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 1
		}
		defer os.RemoveAll(dir)
		out = filepath.Join(dir, "program")
		if isWindows() {
			out += ".exe"
		}
	}
	output, err := clang.BuildIR(ir, out)
	if err != nil {
		fmt.Fprintf(stdout, "%s", output)
		fmt.Fprintf(stderr, "will: link failed\n")
		return 1
	}
	cmd := exec.Command(out)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	return 0
}

func parseBuildArgs(args []string, stderr io.Writer) (buildOptions, int) {
	opts := buildOptions{entry: defaultEntry}
	var paths []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "will: -o needs a path\n")
				return opts, 2
			}
			i++
			opts.out = args[i]
		case "--entry":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "will: --entry needs a name\n")
				return opts, 2
			}
			i++
			opts.entry = args[i]
		case "--emit-ir":
			opts.emitIR = true
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(stderr, "will: unknown flag %s\n", args[i])
				return opts, 2
			}
			paths = append(paths, args[i])
		}
	}
	if len(paths) != 1 {
		fmt.Fprint(stderr, "usage: will build [-o <path>] [--entry <name>] [--emit-ir] <dir or file>\n")
		return opts, 2
	}
	opts.path = paths[0]
	return opts, 0
}

// missingTool reports X0003 for a missing external tool.
func missingTool(stdout io.Writer, name string) int {
	fmt.Fprintf(stdout, "X0003 external tool %s is missing or too old\n    help: install it, or set WILL_CLANG\n", name)
	return 1
}

func defaultOutput(path string) string {
	base := filepath.Base(filepath.Clean(path))
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		base = filepath.Base(path)
	}
	if isWindows() {
		return base + ".exe"
	}
	return base
}

func isWindows() bool {
	return os.PathSeparator == '\\'
}
