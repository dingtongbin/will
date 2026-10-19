package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/index"
	"github.com/will-lang/will/internal/pkg"
)

// runIndex writes the text index of one module directory.
func runIndex(args []string, stdout, stderr io.Writer) int {
	outPath := ""
	var inputs []string
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
			inputs = append(inputs, args[i])
		}
	}
	if len(inputs) != 1 {
		fmt.Fprint(stderr, "usage: will index [--out=<path>] <module dir>\n")
		return 2
	}
	b := pkg.Load(inputs[0])
	if b.Err != nil {
		fmt.Fprintf(stderr, "will: %v\n", b.Err)
		return 1
	}
	all := b.CheckAll()
	if hasError(all) {
		io.WriteString(stdout, diag.FormatAll(all))
		return 1
	}
	text := index.Build(b)
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
