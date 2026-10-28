package main

import (
	"fmt"
	"os"
	"strings"
)

// The kinds of scenario the tool knows.
const (
	// kindProgram runs a program and reads the metric lines it prints.
	kindProgram = "program"
	// kindProcess runs a program and measures the time the process itself
	// takes, which is what the startup scenario wants.
	kindProcess = "process"
	// kindSize builds a program and measures the size of the executable.
	kindSize = "size"
	// kindCompile builds a program and measures the build itself.
	kindCompile = "compile"
)

// scenario is one benchmark of the suite. The C side always exists. The will
// side is missing when the language does not implement what the scenario
// measures; the note says so.
type scenario struct {
	name    string
	kind    string
	desc    string
	cFile   string
	cExtra  []string
	willDir string
	rustDir string
	cflags  []string
	touchC  string
	touchW  string
	note    string
}

// scenarios is the whole suite.
var scenarios = []scenario{
	{
		name:    "compute/int",
		kind:    kindProgram,
		desc:    "integer loop with bit operations and modulo",
		cFile:   "scenarios/compute/int/c/main.c",
		willDir: "scenarios/compute/int/will",
	},
	{
		name:    "compute/fp",
		kind:    kindProgram,
		desc:    "floating point accumulation, multiplication and division",
		cFile:   "scenarios/compute/fp/c/main.c",
		willDir: "scenarios/compute/fp/will",
	},
	{
		name:    "compute/call",
		kind:    kindProgram,
		desc:    "direct call, cross package call, method call and interface dispatch",
		cFile:   "scenarios/compute/call/c/main.c",
		cExtra:  []string{"scenarios/compute/call/c/lib.c"},
		willDir: "scenarios/compute/call/will",
	},
	{
		name:    "compute/ccall",
		kind:    kindProgram,
		desc:    "cost of one call: into C, into the own module, and the clock call",
		cFile:   "scenarios/compute/ccall/c/main.c",
		willDir: "scenarios/compute/ccall/will",
		note:    "a call into C costs the same as a call into C from C; only the inlining is lost",
	},
	{
		name:    "compute/generic",
		kind:    kindProgram,
		desc:    "generic instantiation against a hand written monomorphic function",
		cFile:   "scenarios/compute/generic/c/main.c",
		willDir: "scenarios/compute/generic/will",
	},
	{
		name:    "algo/crc32",
		kind:    kindProgram,
		desc:    "CRC-32 over a byte buffer",
		cFile:   "scenarios/algo/crc32/c/main.c",
		willDir: "scenarios/algo/crc32/will",
	},
	{
		name:    "algo/sort",
		kind:    kindProgram,
		desc:    "quicksort over one thousand and over one million i64 values",
		cFile:   "scenarios/algo/sort/c/main.c",
		willDir: "scenarios/algo/sort/will",
	},
	{
		name:  "algo/sha256",
		kind:  kindProgram,
		desc:  "SHA-256 over a padded message",
		cFile: "scenarios/algo/sha256/c/main.c",
		note:  "the will side waits until both sides read the message the same way",
	},
	{
		name:    "text/utf8",
		kind:    kindProgram,
		desc:    "UTF-8 traversal, splitting and integer parsing",
		cFile:   "scenarios/text/utf8/c/main.c",
		willDir: "scenarios/text/utf8/will",
	},
	{
		name:    "mem/alloc",
		kind:    kindProgram,
		desc:    "one million small object allocations and releases",
		cFile:   "scenarios/mem/alloc/c/main.c",
		willDir: "scenarios/mem/alloc/will",
		note:    "the C side uses the system allocator, the will side the runtime allocator",
	},
	{
		name:    "mem/rc",
		kind:    kindProgram,
		desc:    "reference counted object creation, dereference and release",
		cFile:   "scenarios/mem/rc/c/main.c",
		willDir: "scenarios/mem/rc/will",
		note:    "the C side counts with an atomic reference count",
	},
	{
		name:    "mem/steady",
		kind:    kindProgram,
		desc:    "steady loop that must not allocate",
		cFile:   "scenarios/mem/steady/c/main.c",
		willDir: "scenarios/mem/steady/will",
	},
	{
		name:    "conc/spawn",
		kind:    kindProgram,
		desc:    "one hundred thousand task creations and joins",
		cFile:   "scenarios/conc/spawn/c/main.c",
		willDir: "scenarios/conc/spawn/will",
		note:    "the C side uses one thread per task",
	},
	{
		name:    "conc/chan",
		kind:    kindProgram,
		desc:    "one million messages through a channel",
		cFile:   "scenarios/conc/chan/c/main.c",
		willDir: "scenarios/conc/chan/will",
		note:    "the C side uses a hand written queue",
	},
	{
		name:    "conc/select",
		kind:    kindProgram,
		desc:    "receive from many channels with a select",
		cFile:   "scenarios/conc/select/c/main.c",
		willDir: "scenarios/conc/select/will",
	},
	{
		name:    "conc/tail",
		kind:    kindProgram,
		desc:    "latency percentiles of a send and receive round trip",
		cFile:   "scenarios/conc/tail/c/main.c",
		willDir: "scenarios/conc/tail/will",
	},
	{
		name:    "io/file",
		kind:    kindProgram,
		desc:    "sequential and random file reads and writes",
		cFile:   "scenarios/io/file/c/main.c",
		willDir: "scenarios/io/file/will",
		note:    "the will side uses std/os/fs",
		cflags:  []string{"-lws2_32"},
	},
	{
		name:   "io/net",
		kind:   kindProgram,
		desc:   "TCP echo throughput and connection count",
		cFile:  "scenarios/io/net/c/main.c",
		note:   "the will side waits for the network package of the standard library",
		cflags: []string{"-lws2_32"},
	},
	{
		name:    "startup/cold",
		kind:    kindProcess,
		desc:    "time from the start of the process to the end of an empty program",
		cFile:   "scenarios/startup/cold/c/main.c",
		willDir: "scenarios/startup/cold/will",
	},
	{
		name:    "size",
		kind:    kindSize,
		desc:    "size of the executable of an empty program",
		cFile:   "scenarios/size/size/c/main.c",
		willDir: "scenarios/size/size/will",
	},
	{
		name:    "compile/throughput",
		kind:    kindCompile,
		desc:    "lines per second of the compiler against the C compiler",
		cFile:   "scenarios/compile/corpus/c/main.c",
		cExtra:  []string{"scenarios/compile/corpus/c/lib.c"},
		willDir: "scenarios/compile/corpus/will",
	},
	{
		name:    "compile/incremental",
		kind:    kindCompile,
		desc:    "time of an incremental build after one file changed",
		cFile:   "scenarios/compile/corpus/c/main.c",
		cExtra:  []string{"scenarios/compile/corpus/c/lib.c"},
		willDir: "scenarios/compile/corpus/will",
		touchC:  "scenarios/compile/corpus/c/lib.c",
		touchW:  "scenarios/compile/corpus/will/lib/lib.will",
	},
	{
		name:    "compile/memory",
		kind:    kindCompile,
		desc:    "peak memory of the compiler against the C compiler",
		cFile:   "scenarios/compile/corpus/c/main.c",
		cExtra:  []string{"scenarios/compile/corpus/c/lib.c"},
		willDir: "scenarios/compile/corpus/will",
		note:    "the peak memory is not readable on windows yet",
	},
	{
		name:    "checks/int",
		kind:    kindProgram,
		desc:    "cost of the integer checks of the language",
		cFile:   "scenarios/checks/int/c/main.c",
		willDir: "scenarios/checks/int/will",
		rustDir: "scenarios/checks/int/rust",
		note:    "the will side always checks, the C side does not",
	},
	{
		name:    "checks/clone",
		kind:    kindProgram,
		desc:    "cost of copying a value that has to be copied",
		cFile:   "scenarios/checks/clone/c/main.c",
		willDir: "scenarios/checks/clone/will",
	},
}

// selectScenarios returns the scenarios to run, in the order of the suite.
func selectScenarios(names []string) []scenario {
	if len(names) == 0 {
		return scenarios
	}
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	var out []scenario
	for _, sc := range scenarios {
		if wanted[sc.name] {
			out = append(out, sc)
		}
	}
	return out
}

// listScenarios prints the suite and the state of both sides of each
// scenario.
func listScenarios(root string, stdout *os.File) {
	fmt.Fprintf(stdout, "%-22s %-10s %-8s %-8s %s\n", "scenario", "kind", "c", "will", "description")
	for _, sc := range scenarios {
		will := "no"
		if sc.willDir != "" {
			will = "yes"
		}
		fmt.Fprintf(stdout, "%-22s %-10s %-8s %-8s %s\n", sc.name, sc.kind, "yes", will, sc.desc)
	}
}

// resultPath returns the csv path of one scenario.
func resultPath(dir string, sc scenario) string {
	return dir + string(os.PathSeparator) + strings.ReplaceAll(sc.name, "/", string(os.PathSeparator)) + ".csv"
}
