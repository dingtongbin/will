//go:build linux

package main

import (
	"os"
	"strings"
)

// cpuModel returns the processor name of this host.
func cpuModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "unknown"
}

// totalMemory returns the physical memory of this host.
func totalMemory() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				amount, err := parseUint(fields[1])
				if err == nil {
					return formatBytes(amount * 1024)
				}
			}
		}
	}
	return "unknown"
}
