package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/will-lang/will/internal/diag"
)

// buildOptions is one build or run invocation.
type buildOptions struct {
	path         string
	entry        string
	out          string
	opt          string
	debug        bool
	target       string
	targetSet    bool
	lib          bool
	freestanding bool
	features     string
	disabled     map[string]bool
	json         bool
	long         bool
	werror       bool
}

// parseBuildArgs reads the flags of will build and will run. The arguments
// after "--" belong to the program that will run.
func parseBuildArgs(command string, args []string, stderr io.Writer) (buildOptions, []string, int) {
	opts := buildOptions{entry: defaultEntry, opt: defaultOpt(command), debug: command == "run",
		disabled: map[string]bool{}}
	var paths, rest []string
	for i := 0; i < len(args); i++ {
		name, inline, hasInline := splitFlag(args[i])
		switch name {
		case "--":
			rest = append(rest, args[i+1:]...)
			i = len(args)
		case "-o":
			value, next, ok := flagValue(args, i, inline, hasInline, "-o", stderr)
			if !ok {
				return opts, nil, 2
			}
			i, opts.out = next, value
		case "--entry":
			value, next, ok := flagValue(args, i, inline, hasInline, "--entry", stderr)
			if !ok {
				return opts, nil, 2
			}
			i, opts.entry = next, value
		case "-O0", "-O1", "-O2", "-O3":
			opts.opt = name
		case "-g":
			opts.debug = true
		case "--target":
			value, next, ok := flagValue(args, i, inline, hasInline, "--target", stderr)
			if !ok {
				return opts, nil, 2
			}
			i, opts.target, opts.targetSet = next, value, true
		case "--lib":
			opts.lib = true
		case "--freestanding":
			opts.freestanding = true
		case "--features":
			value, next, ok := flagValue(args, i, inline, hasInline, "--features", stderr)
			if !ok {
				return opts, nil, 2
			}
			i, opts.features = next, value
		case "--json":
			opts.json = true
		case "--long":
			opts.long = true
		case "-Werror":
			opts.werror = true
		default:
			switch {
			case strings.HasPrefix(args[i], "-Wno-"):
				family := strings.TrimPrefix(args[i], "-Wno-")
				if !diag.KnownFamily(family) {
					fmt.Fprintf(stderr, "will: unknown warning family %s\n", family)
					return opts, nil, 2
				}
				opts.disabled[family] = true
			case strings.HasPrefix(args[i], "-W"):
				family := strings.TrimPrefix(args[i], "-W")
				if !diag.KnownFamily(family) {
					fmt.Fprintf(stderr, "will: unknown warning family %s\n", family)
					return opts, nil, 2
				}
				delete(opts.disabled, family)
			case strings.HasPrefix(args[i], "-"):
				fmt.Fprintf(stderr, "will: unknown flag %s\n", args[i])
				return opts, nil, 2
			default:
				paths = append(paths, args[i])
			}
		}
	}
	if len(paths) != 1 {
		fmt.Fprintf(stderr, "usage: will %s [-o <path>] [--entry <name>] [-O0..-O3] [--target <triple>] <dir or file>\n", command)
		return opts, nil, 2
	}
	opts.path = paths[0]
	return opts, rest, 0
}

// defaultOpt returns the optimization level a command starts with.
func defaultOpt(command string) string {
	if command == "run" {
		return "-O0"
	}
	return "-O2"
}

// flagValue reads the value of a flag, either from "--name=value" or from the
// next argument.
func flagValue(args []string, i int, inline string, hasInline bool, flag string, stderr io.Writer) (string, int, bool) {
	if hasInline {
		return inline, i, true
	}
	if i+1 >= len(args) {
		fmt.Fprintf(stderr, "will: %s needs a value\n", flag)
		return "", i, false
	}
	return args[i+1], i + 1, true
}
