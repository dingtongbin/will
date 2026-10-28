//go:build !windows && !linux && !darwin

package main

import (
	"os/exec"
)

// memoryTracker follows one child process and reports its peak memory. The
// platform has no way to report it, so the tracker reports zero.
type memoryTracker struct{}

func newMemoryTracker() *memoryTracker {
	return &memoryTracker{}
}

// attach does nothing.
func (t *memoryTracker) attach(cmd *exec.Cmd) {
}

// peakBytes returns the peak memory of the finished command.
func (t *memoryTracker) peakBytes() uint64 {
	return 0
}

// cpuModel returns the processor name of this host.
func cpuModel() string {
	return "unknown"
}

// totalMemory returns the physical memory of this host.
func totalMemory() string {
	return "unknown"
}
