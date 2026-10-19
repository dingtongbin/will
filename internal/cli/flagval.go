package cli

import "strings"

// splitFlag splits "--name=value" into its name and value. A flag written
// without "=" returns hasValue false, so the caller reads the next argument.
func splitFlag(arg string) (name, value string, hasValue bool) {
	if i := strings.IndexByte(arg, '='); i > 0 {
		return arg[:i], arg[i+1:], true
	}
	return arg, "", false
}
