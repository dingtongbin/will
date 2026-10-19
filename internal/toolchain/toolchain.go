// Package toolchain calls the external tools the build needs. clang turns
// the LLVM text into an object or an executable.
package toolchain

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ErrNotFound reports that an external tool is missing.
var ErrNotFound = errors.New("external tool not found")

// Clang is the C and LLVM driver.
type Clang struct {
	Path string
}

// FindClang locates clang. WILL_CLANG overrides the search.
func FindClang() (*Clang, error) {
	if path := os.Getenv("WILL_CLANG"); path != "" {
		return &Clang{Path: path}, nil
	}
	path, err := exec.LookPath("clang")
	if err != nil {
		return nil, fmt.Errorf("%w: clang", ErrNotFound)
	}
	return &Clang{Path: path}, nil
}

// BuildIR compiles LLVM text into an output file. It returns the combined
// output of the tool for diagnostics.
func (c *Clang) BuildIR(ir []byte, out string, args ...string) (string, error) {
	dir, err := os.MkdirTemp("", "will-ir")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	irPath := filepath.Join(dir, "module.ll")
	if err := os.WriteFile(irPath, ir, 0o644); err != nil {
		return "", err
	}
	cmdArgs := []string{"-x", "ir", irPath, "-o", out}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command(c.Path, cmdArgs...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// AssembleIR compiles LLVM text to assembly text. The input goes through
// standard input, so no temporary file is needed.
func (c *Clang) AssembleIR(ir []byte, args ...string) (string, error) {
	cmdArgs := []string{"-x", "ir", "-S", "-o", "-", "-"}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command(c.Path, cmdArgs...)
	cmd.Stdin = bytes.NewReader(ir)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, errBuf.String())
	}
	return out.String(), nil
}

// Link links one output with extra inputs.
func (c *Clang) Link(out string, inputs ...string) (string, error) {
	args := append([]string{}, inputs...)
	args = append(args, "-o", out)
	cmd := exec.Command(c.Path, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
