// Command bench builds and runs the language level benchmarks of the will
// suite and writes the results as csv.
//
// The method and the rules are in the reasoning volume about the comparison
// with C. The tool lives under bench/ because the project layout reserves
// tools/bench for the same job and the suite must stay self contained.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options are the settings one bench run uses.
type options struct {
	runs     int
	cc       string
	will     string
	out      string
	scopes   scenarioList
	list     bool
	summary  bool
	keep     bool
	compiler string
}

// scenarioList collects the scenario names given on the command line.
type scenarioList []string

func (l *scenarioList) String() string {
	return strings.Join(*l, ",")
}

func (l *scenarioList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

func run(args []string, stdout, stderr *os.File) int {
	opts := options{runs: 5}
	flags := flag.NewFlagSet("bench", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.IntVar(&opts.runs, "runs", 5, "independent runs per program")
	flags.StringVar(&opts.cc, "cc", "", "C compiler (default $CC, then clang, then cc)")
	flags.StringVar(&opts.will, "will", "", "will compiler (default $WILL_BIN, then will)")
	flags.StringVar(&opts.out, "out", "", "results directory (default bench/results/<target>)")
	flags.Var(&opts.scopes, "scenario", "scenario to run, repeatable (default all)")
	flags.BoolVar(&opts.list, "list", false, "list the scenarios and exit")
	flags.BoolVar(&opts.summary, "summary", false, "print the summary table of the results and exit")
	flags.BoolVar(&opts.keep, "keep", false, "keep the build directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if opts.runs < 1 {
		fmt.Fprintf(stderr, "bench: -runs needs a positive count\n")
		return 2
	}
	root, err := findBenchRoot()
	if err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}
	if opts.list {
		listScenarios(root, stdout)
		return 0
	}
	if opts.summary {
		return summarize(root, opts, stdout, stderr)
	}
	return measure(root, opts, stdout, stderr)
}
