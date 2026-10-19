// Command gate runs the build gate. The fast tier is the unit tests; the full
// tier adds the source checks and the no-assembly rule. A gate run owns the
// machine: do not run another gate or a benchmark at the same time.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	full := flag.Bool("full", false, "run the full tier")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	fail := false
	if *full {
		if err := runCheckcode(root); err != nil {
			fmt.Fprintf(os.Stderr, "gate: checkcode: %v\n", err)
			fail = true
		}
		if err := runNoAssembly(root); err != nil {
			fmt.Fprintf(os.Stderr, "gate: no-assembly: %v\n", err)
			fail = true
		}
		if err := runCorpus(root); err != nil {
			fmt.Fprintf(os.Stderr, "gate: corpus: %v\n", err)
			fail = true
		}
	}
	if err := runTests(); err != nil {
		fmt.Fprintf(os.Stderr, "gate: tests: %v\n", err)
		fail = true
	}
	if fail {
		os.Exit(1)
	}
	fmt.Println("gate: ok")
}

func runCheckcode(root string) error {
	cmd := exec.Command("go", "run", "./tools/checkcode", root)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runTests() error {
	cmd := exec.Command("go", "test", "./...")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runNoAssembly rejects any assembly file in the tree: the runtime is portable
// C11 and the platform layer is C, so an assembly file means a rule was broken.
func runNoAssembly(root string) error {
	found, err := findAssembly(root)
	if err != nil {
		return err
	}
	if len(found) > 0 {
		return fmt.Errorf("assembly files are not allowed: %s", strings.Join(found, ", "))
	}
	return nil
}

// findAssembly lists the assembly files under root.
func findAssembly(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "build", "desc", ".opencode":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".s", ".asm":
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out, err
}
