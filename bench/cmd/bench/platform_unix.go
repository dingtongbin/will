//go:build linux || darwin

package main

import (
	"os/exec"
	"syscall"
)

// memoryTracker follows one child process and reports its peak memory. The
// peak comes from the wait status of the process, so the tracker keeps only
// the command.
type memoryTracker struct {
	cmd *exec.Cmd
}

func newMemoryTracker() *memoryTracker {
	return &memoryTracker{}
}

// attach keeps the command, so the peak can be read after the wait.
func (t *memoryTracker) attach(cmd *exec.Cmd) {
	t.cmd = cmd
}

// peakBytes returns the peak resident set size of the finished command.
func (t *memoryTracker) peakBytes() uint64 {
	if t.cmd == nil || t.cmd.ProcessState == nil {
		return 0
	}
	usage, ok := t.cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil {
		return 0
	}
	// Linux reports the peak in kilobytes, macOS in bytes.
	return uint64(usage.Maxrss) * 1024
}
