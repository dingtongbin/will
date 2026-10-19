package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/mod"
	"github.com/will-lang/will/internal/pkg"
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

// The root argument is resolved by pkg.Load, so no separate reader is needed.

// runModVerify checks every checksum of the modules a local replace selected.
// A dependency that was fetched into the module cache is left to will get,
// which will add its own comparison.
func runModVerify(args []string, stdout, stderr io.Writer) int {
	root, code := modRoot(args, stderr)
	if code != 0 {
		return code
	}
	b := pkg.LoadWithoutSums(root)
	if b.Err != nil {
		return loadFailure(b.Err, stderr)
	}
	data, err := os.ReadFile(filepath.Join(b.Root, "will.sum"))
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	lines, ds := mod.ParseSum("will.sum", data)
	var bag diag.Bag
	bag.AddAll(ds)
	byPath := map[string]mod.SumLine{}
	for _, line := range lines {
		if _, seen := byPath[line.Path]; !seen {
			byPath[line.Path] = line
		}
	}
	for _, lm := range b.LocalModules {
		line, ok := byPath[lm.Path]
		if !ok {
			bag.Error(source.Pos{File: "will.sum", Line: 1, Col: 1},
				diag.CodeChecksumMismatch, "no checksum for "+lm.Path, "run will mod tidy")
			continue
		}
		got, err := mod.SourceFaceHash(lm.Dir)
		if err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 1
		}
		if want := "sha256:" + got; want != line.Sum {
			bag.Error(source.Pos{File: "will.sum", Line: line.Line, Col: 1},
				diag.CodeChecksumMismatch, "checksum mismatch for "+lm.Path, "run will mod tidy")
		}
	}
	all := bag.Diagnostics()
	if len(all) > 0 {
		io.WriteString(stdout, diag.FormatAll(all))
		return 1
	}
	return 0
}

// runModTidy rewrites will.sum for the whole module graph: every module a local
// replace selected gets a fresh checksum, and a line whose dependency is not
// local is kept as it was.
func runModTidy(args []string, stdout, stderr io.Writer) int {
	root, code := modRoot(args, stderr)
	if code != 0 {
		return code
	}
	b := pkg.LoadWithoutSums(root)
	if b.Err != nil {
		return loadFailure(b.Err, stderr)
	}
	local := map[string]bool{}
	for _, lm := range b.LocalModules {
		local[lm.Path] = true
	}
	var lines []mod.SumLine
	if data, err := os.ReadFile(filepath.Join(b.Root, "will.sum")); err == nil {
		kept, ds := mod.ParseSum("will.sum", data)
		if len(ds) > 0 {
			io.WriteString(stdout, diag.FormatAll(ds))
			return 1
		}
		for _, line := range kept {
			if !local[line.Path] {
				lines = append(lines, line)
			}
		}
	}
	for _, lm := range b.LocalModules {
		hash, err := mod.SourceFaceHash(lm.Dir)
		if err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 1
		}
		lines = append(lines, mod.SumLine{Path: lm.Path, Sum: "sha256:" + hash})
	}
	if err := os.WriteFile(filepath.Join(b.Root, "will.sum"), mod.FormatSum(lines), 0o644); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	return 0
}

// The module graph lists the local modules, so no separate require scan is
// needed.
