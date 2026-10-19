package cli

import (
	"fmt"
	"io"

	"github.com/will-lang/will/internal/pkg"
)

// loadFailure reports a module load fault and returns the exit code. A missing
// bundled standard library is a broken toolchain, not a source error, so it
// prints one line on standard error and exits 2. Every other load fault is a
// source error and exits 1.
func loadFailure(err error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "will: %v\n", err)
	if pkg.IsMissingBundledStd(err) {
		return 2
	}
	return 1
}
