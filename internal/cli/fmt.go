package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	willfmt "github.com/will-lang/will/internal/fmt"
)

// runFmt writes every file in canonical form, or only reports the files that
// are not canonical when --check is set. It is the only command that writes
// source files.
func runFmt(args []string, stdout, stderr io.Writer) int {
	check := false
	var files []string
	for _, a := range args {
		switch {
		case a == "--check":
			check = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "will: unknown flag %s\n", a)
			return 2
		default:
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		fmt.Fprint(stderr, "usage: will fmt [--check] <file>...\n")
		return 2
	}
	unformatted := false
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 1
		}
		out := willfmt.Source(data)
		if bytes.Equal(data, out) {
			continue
		}
		if check {
			fmt.Fprintf(stdout, "will: %s is not formatted\n", path)
			unformatted = true
			continue
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 1
		}
	}
	if unformatted {
		return 1
	}
	return 0
}
