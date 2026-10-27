package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/pkg"
)

// runTest discovers the Test* and Benchmark* functions of the root package,
// writes one generated entry beside them, and runs it through the normal build
// and run path. The generated file is removed even when the run fails.
func runTest(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprint(stderr, "usage: will test [<dir>]\n")
		return 2
	}
	path := "."
	if len(args) == 1 {
		path = args[0]
	}
	b := pkg.Load(path)
	if b.Err != nil {
		fmt.Fprintf(stderr, "will: %v\n", b.Err)
		return 1
	}
	hasErrors := false
	for _, d := range b.Diags {
		if d.Severity == diag.Error {
			fmt.Fprintln(stderr, diag.Format(d))
			hasErrors = true
		}
	}
	if hasErrors {
		return 1
	}
	root := rootPackage(b)
	if root == nil {
		fmt.Fprintln(stderr, "will: no root package")
		return 1
	}
	var tests []string
	var benchmarks []string
	for _, f := range root.Files {
		if f.AST == nil {
			continue
		}
		for _, d := range f.AST.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || len(fn.Params) != 1 {
				continue
			}
			param := ast.TypeText(fn.Params[0].Type)
			switch {
			case strings.HasPrefix(fn.Name, "Test") && len(fn.Name) > len("Test") && isTestHandle(param, "T"):
				tests = append(tests, fn.Name)
			case strings.HasPrefix(fn.Name, "Benchmark") && len(fn.Name) > len("Benchmark") && isTestHandle(param, "B"):
				benchmarks = append(benchmarks, fn.Name)
			}
		}
	}
	if len(tests) == 0 && len(benchmarks) == 0 {
		fmt.Fprintf(stdout, "ok %s 0/0\n", b.Mod.Path)
		return 0
	}
	file := filepath.Join(root.Dir, "zz_will_test_main.will")
	if _, err := os.Stat(file); err == nil {
		fmt.Fprintf(stderr, "will: generated test entry %s already exists\n", file)
		return 1
	}
	if err := os.WriteFile(file, []byte(testEntry(b.Mod.Path, tests, benchmarks)), 0644); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	defer os.Remove(file)
	code := runRun([]string{"--entry", "will_test_main", root.Dir}, stdout, stderr)
	if code != 0 {
		return 1
	}
	return 0
}

// rootPackage finds the package named by the module path in the loaded graph.
func rootPackage(b *pkg.Build) *pkg.Package {
	for _, p := range b.Packages {
		if p.Path == b.Mod.Path {
			return p
		}
	}
	if len(b.Packages) == 1 {
		return b.Packages[0]
	}
	return nil
}

// isTestHandle accepts both the local T spelling and a qualified test.T.
func isTestHandle(text, name string) bool {
	return text == name || strings.HasSuffix(text, "."+name)
}

// testEntry writes a serial runner. Fatal still ends the process, while a
// Fail marks the shared state and lets the runner continue to the next case.
func testEntry(module string, tests, benchmarks []string) string {
	var out strings.Builder
	out.WriteString("import \"std/test\"\n\ndef will_test_main() -> i32 {\n")
	out.WriteString("    var passed: i32 = 0\n")
	total := len(tests) + len(benchmarks)
	fmt.Fprintf(&out, "    var total: i32 = %d\n", total)
	for _, name := range tests {
		label := strconv.Quote(module + "/" + name)
		fmt.Fprintf(&out, "    var %s: test.T = test.new_t(%s)\n", testVar(name), label)
		fmt.Fprintf(&out, "    %s(%s)\n", name, testVar(name))
		fmt.Fprintf(&out, "    if %s.failed() {\n", testVar(name))
		fmt.Fprintf(&out, "        print(\"FAIL \", %s, \"\\n\")\n", label)
		out.WriteString("    } else {\n        passed += 1\n    }\n")
	}
	for _, name := range benchmarks {
		label := strconv.Quote(module + "/" + name)
		fmt.Fprintf(&out, "    var %s: test.B = test.new_b(%s)\n", benchVar(name), label)
		fmt.Fprintf(&out, "    %s(%s)\n", name, benchVar(name))
		out.WriteString("    passed += 1\n")
	}
	fmt.Fprintf(&out, "    if passed == total {\n        print(\"ok \", %s, \" \", passed, \"/\", total, \"\\n\")\n        return 0\n    }\n",
		strconv.Quote(module))
	out.WriteString("    return 1\n}\n")
	return out.String()
}

func testVar(name string) string {
	return "t_" + strings.TrimPrefix(name, "Test")
}

func benchVar(name string) string {
	return "b_" + strings.TrimPrefix(name, "Benchmark")
}
