package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/mod"
	"github.com/will-lang/will/internal/source"
)

const modUsage = "usage: will mod <tidy|verify> [<module dir>]\n"

// runMod dispatches the two will.mod and will.sum commands.
func runMod(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, modUsage)
		return 2
	}
	switch args[0] {
	case "verify":
		return runModVerify(args[1:], stdout, stderr)
	case "tidy":
		return runModTidy(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "will: unknown mod command %s\n", args[0])
	fmt.Fprint(stderr, modUsage)
	return 2
}

// modRoot reads the one optional directory argument.
func modRoot(args []string, stderr io.Writer) (string, int) {
	if len(args) > 1 || (len(args) == 1 && strings.HasPrefix(args[0], "-")) {
		fmt.Fprint(stderr, modUsage)
		return "", 2
	}
	if len(args) == 1 {
		return args[0], 0
	}
	return ".", 0
}

// loadModule reads will.mod of one directory.
func loadModule(root string, stderr io.Writer) (*mod.Module, int) {
	data, err := os.ReadFile(filepath.Join(root, "will.mod"))
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, 1
	}
	m, ds := mod.Parse("will.mod", data)
	if len(ds) > 0 {
		io.WriteString(stderr, diag.FormatAll(ds))
		return nil, 1
	}
	return m, 0
}

// runModVerify checks every checksum it can compute. A dependency that is not
// a local directory cannot be hashed here, so it is left alone until it is
// fetched by will get.
func runModVerify(args []string, stdout, stderr io.Writer) int {
	root, code := modRoot(args, stderr)
	if code != 0 {
		return code
	}
	m, code := loadModule(root, stderr)
	if code != 0 {
		return code
	}
	sum, err := os.ReadFile(filepath.Join(root, "will.sum"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	lines, ds := mod.ParseSum("will.sum", sum)
	var bag diag.Bag
	bag.AddAll(ds)
	for _, line := range lines {
		dir, local := mod.LocalDir(root, m, line.Path)
		if !local {
			continue
		}
		got, err := mod.SourceFaceHash(dir)
		if err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 1
		}
		if want := "sha256:" + got; want != line.Sum {
			bag.Error(source.Pos{File: "will.sum", Line: line.Line, Col: 1},
				diag.CodeChecksumMismatch, "checksum mismatch for "+line.Path, "run will mod tidy")
		}
	}
	all := bag.Diagnostics()
	if len(all) > 0 {
		io.WriteString(stdout, diag.FormatAll(all))
		return 1
	}
	return 0
}

// runModTidy rewrites will.sum: the checksums it can compute are refreshed,
// and a line whose dependency is not local is kept as it was.
func runModTidy(args []string, stdout, stderr io.Writer) int {
	root, code := modRoot(args, stderr)
	if code != 0 {
		return code
	}
	m, code := loadModule(root, stderr)
	if code != 0 {
		return code
	}
	var kept []mod.SumLine
	if data, err := os.ReadFile(filepath.Join(root, "will.sum")); err == nil {
		lines, ds := mod.ParseSum("will.sum", data)
		if len(ds) > 0 {
			io.WriteString(stdout, diag.FormatAll(ds))
			return 1
		}
		kept = lines
	}
	lines := make([]mod.SumLine, 0, len(kept))
	for _, l := range kept {
		if _, local := mod.LocalDir(root, m, l.Path); local {
			continue
		}
		lines = append(lines, l)
	}
	seen := map[string]bool{}
	for _, dep := range requiredPaths(m) {
		dir, local := mod.LocalDir(root, m, dep)
		if !local || seen[dep] {
			continue
		}
		seen[dep] = true
		hash, err := mod.SourceFaceHash(dir)
		if err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 1
		}
		lines = append(lines, mod.SumLine{Path: dep, Sum: "sha256:" + hash})
	}
	if err := os.WriteFile(filepath.Join(root, "will.sum"), mod.FormatSum(lines), 0o644); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	return 0
}

// requiredPaths lists the import paths of every require, in writing order.
func requiredPaths(m *mod.Module) []string {
	var out []string
	for _, r := range m.Requires {
		out = append(out, r.Path)
	}
	return out
}
