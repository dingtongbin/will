package pkg

import (
	"errors"
	"os"
	"path/filepath"
)

// MissingBundledStd reports that the bundled standard library is not on disk.
// It is a toolchain fault, not a source error, so the command line turns it
// into a startup error with exit code 2 and does not report B0008.
type MissingBundledStd struct {
	Path string
}

// Error names the standard library root that is missing.
func (e *MissingBundledStd) Error() string {
	return "the bundled standard library is missing: " + e.Path + " (set WILL_ROOT)"
}

// IsMissingBundledStd reports whether err is the missing standard library
// fault.
func IsMissingBundledStd(err error) bool {
	var e *MissingBundledStd
	return errors.As(err, &e)
}

// bundledStdRoot returns the root of the bundled standard library and whether
// that directory exists. WILL_ROOT overrides the layout. Otherwise the root is
// ../std beside the running will binary, which is <inst>/std for an installed
// <inst>/bin/will and <repo>/std for <repo>/build/will.
func bundledStdRoot() (string, bool) {
	dir := os.Getenv("WILL_ROOT")
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return filepath.Clean(filepath.Join("..", "std")), false
		}
		return statDir(stdRootBeside(exe))
	}
	return statDir(filepath.Clean(dir))
}

// isStdRoot is not needed once WILL_ROOT names the standard library root.

func statDir(dir string) (string, bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return dir, false
	}
	return dir, true
}

// stdRootBeside is the root the layout gives for one will binary:
// <binary dir>/../std.
func stdRootBeside(exe string) string {
	return filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "std"))
}
