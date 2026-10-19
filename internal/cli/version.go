package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// CompilerVersion is the language version this compiler implements.
const CompilerVersion = "0.0.0-dev"

// runVersion prints the build identity: version, target, optimization level,
// and the hash of the running binary.
func runVersion(stdout io.Writer) int {
	fmt.Fprintf(stdout, "will %s\n", CompilerVersion)
	fmt.Fprintf(stdout, "target %s\n", targetTriple())
	fmt.Fprintln(stdout, "opt -O0")
	fmt.Fprintf(stdout, "binary %s\n", binaryHash())
	return 0
}

func binaryHash() string {
	path, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
