package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// machineInfo is the description of the machine the results come from. The
// same description is written to machine.txt next to the results.
type machineInfo struct {
	cpu       string
	cores     int
	memory    string
	os        string
	compiler  string
	will      string
	frequency string
}

// describeMachine collects the machine description of this host.
func describeMachine(cc string, will string) machineInfo {
	willVersion := "not found"
	if will != "" {
		if output, err := exec.Command(will, "version").Output(); err == nil {
			willVersion = firstLine(string(output))
		}
	}
	compiler := "not found"
	if cc != "" {
		compiler = compilerVersion(cc)
	}
	return machineInfo{
		cpu:       cpuModel(),
		cores:     runtime.NumCPU(),
		memory:    totalMemory(),
		os:        runtime.GOOS + "/" + runtime.GOARCH,
		compiler:  compiler,
		will:      willVersion,
		frequency: "not controlled",
	}
}

// writeMachine writes the machine description of the target.
func writeMachine(path string, info machineInfo) error {
	text := fmt.Sprintf("cpu: %s\ncores: %d\nmemory: %s\nos: %s\ncompiler: %s\nwill: %s\nfrequency policy: %s\n",
		info.cpu, info.cores, info.memory, info.os, info.compiler, info.will, info.frequency)
	return os.WriteFile(path, []byte(text), 0o644)
}

// firstLine returns the first line of a text, trimmed.
func firstLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

// formatBytes writes a byte count in the largest unit that keeps it exact.
func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return formatUint(bytes) + " B"
	}
	div := uint64(unit)
	exp := 0
	for n := bytes / unit; n >= unit && exp < 3; n /= unit {
		div *= unit
		exp++
	}
	return formatFloat(float64(bytes)/float64(div)) + " " + string("KMGT"[exp]) + "iB"
}

// formatUint writes a number in decimal.
func formatUint(value uint64) string {
	return strconv.FormatUint(value, 10)
}

// formatFloat writes a number with one decimal.
func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64)
}

// parseUint reads a decimal number.
func parseUint(text string) (uint64, error) {
	if text == "" {
		return 0, fmt.Errorf("bench: an empty string is not a number")
	}
	var out uint64
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("bench: %q is not a number", text)
		}
		out = out*10 + uint64(digit-'0')
	}
	return out, nil
}
