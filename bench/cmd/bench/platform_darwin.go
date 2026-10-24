//go:build darwin

package main

import (
	"syscall"
)

// cpuModel returns the processor name of this host.
func cpuModel() string {
	name, err := syscall.Sysctl("machdep.cpu.brand_string")
	if err != nil {
		return "unknown"
	}
	return name
}

// totalMemory returns the physical memory of this host.
func totalMemory() string {
	bytes, err := syscall.SysctlUint64("hw.memsize")
	if err != nil {
		return "unknown"
	}
	return formatBytes(bytes)
}
