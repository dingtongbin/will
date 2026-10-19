package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/will-lang/will/internal/lsp"
)

// runLSP serves the language server on the process's standard streams. The
// protocol owns standard output, so nothing else may write there while it
// runs; diagnostics and progress go to the client as protocol messages.
func runLSP(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprint(stderr, "usage: will lsp\n")
		return 2
	}
	conn := &stdioConn{in: os.Stdin, out: os.Stdout}
	if err := lsp.Serve(conn); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	return 0
}

// stdioConn joins the two standard streams into one connection.
type stdioConn struct {
	in  io.Reader
	out io.Writer
}

func (c *stdioConn) Read(p []byte) (int, error)  { return c.in.Read(p) }
func (c *stdioConn) Write(p []byte) (int, error) { return c.out.Write(p) }
func (c *stdioConn) Close() error                { return nil }
