package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/will-lang/will/internal/willc"
)

// translateError renders a willc or generator failure in the G0001 form the
// code table fixes for a node the mechanical translation cannot handle.
func translateError(err error) string {
	const help = "    help: this is a compiler bug; report it with the willc file\n"
	var we *willc.Error
	if errors.As(err, &we) {
		if we.ID > 0 {
			return fmt.Sprintf("G0001 cannot translate node #%d: %s\n%s", we.ID, we.Message, help)
		}
		return "G0001 " + we.Message + "\n" + help
	}
	return "G0001 " + err.Error() + "\n" + help
}

// reportTranslate prints the G0001 form of a willc or generator error.
func reportTranslate(stdout io.Writer, err error) {
	io.WriteString(stdout, translateError(err))
}
