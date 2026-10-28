package cli

import (
	"fmt"
	"io"
)

// loadFailure reports a module load fault on standard error and returns the
// source-error exit code. A missing dependency is a normal build error now that
// the toolchain provides no packages of its own.
func loadFailure(err error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "will: %v\n", err)
	return 1
}
