package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const mainSource = `func main() {
}
`

// runNew writes a new module: will.mod and src/main.will.
func runNew(args []string, stdout, stderr io.Writer) int {
	var paths []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(stderr, "will: unknown flag %s\n", a)
			return 2
		}
		paths = append(paths, a)
	}
	if len(paths) != 1 {
		fmt.Fprint(stderr, "usage: will new <module path>\n")
		return 2
	}
	modulePath := strings.TrimSuffix(paths[0], "/")
	if modulePath == "" || filepath.IsAbs(modulePath) {
		fmt.Fprint(stderr, "will: the module path must be a relative import path\n")
		return 2
	}
	dir := filepath.FromSlash(modulePath)
	if filepath.Base(dir) == "." {
		fmt.Fprint(stderr, "will: the module path needs a last segment\n")
		return 2
	}
	willMod := filepath.Join(dir, "will.mod")
	if _, err := os.Stat(willMod); err == nil {
		fmt.Fprintf(stderr, "will: %s already exists\n", willMod)
		return 1
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	content := fmt.Sprintf("module %s\nwill 1\n", modulePath)
	if err := os.WriteFile(willMod, []byte(content), 0o644); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.will"), []byte(mainSource), 0o644); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created %s\n", dir)
	return 0
}
