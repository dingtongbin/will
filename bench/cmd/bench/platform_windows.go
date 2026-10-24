//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

const (
	// processQueryInformation and processVMRead are the rights the tracker
	// needs to read the memory of a process it did not start.
	processQueryInformation = 0x0400
	processVMRead           = 0x0010
)

// memoryTracker follows one child process and reports its peak memory. The
// tracker opens its own query handle, because the handle of the process is
// gone once the process is done.
type memoryTracker struct {
	query    syscall.Handle
	done     chan struct{}
	finished chan struct{}
	peak     uint64
}

func newMemoryTracker() *memoryTracker {
	return &memoryTracker{}
}

type memoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// attach opens a query handle and samples the working set while the process
// runs. K32GetProcessMemoryInfo cannot be read reliably after the process has
// exited, so the peak is collected by polling before Wait returns.
func (t *memoryTracker) attach(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	open := syscall.NewLazyDLL("kernel32.dll").NewProc("OpenProcess")
	handle, _, _ := open.Call(processQueryInformation|processVMRead, 0, uintptr(cmd.Process.Pid))
	if handle == 0 {
		return
	}
	t.query = syscall.Handle(handle)
	t.done = make(chan struct{})
	t.finished = make(chan struct{})
	go func() {
		defer close(t.finished)
		proc := syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-ticker.C:
				var counters memoryCounters
				counters.cb = uint32(unsafe.Sizeof(counters))
				ok, _, _ := proc.Call(uintptr(t.query),
					uintptr(unsafe.Pointer(&counters)), unsafe.Sizeof(counters))
				if ok == 0 {
					return
				}
				if uint64(counters.peakWorkingSetSize) > t.peak {
					t.peak = uint64(counters.peakWorkingSetSize)
				}
			}
		}
	}()
}

// peakBytes stops the sampler and returns the largest working set it saw.
func (t *memoryTracker) peakBytes() uint64 {
	if t.query == 0 {
		return 0
	}
	close(t.done)
	<-t.finished
	syscall.CloseHandle(t.query)
	t.query = 0
	return t.peak
}

// cpuModel returns the processor name of this host.
func cpuModel() string {
	if name, ok := registryString(syscall.HKEY_LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "ProcessorNameString"); ok {
		return name
	}
	return "unknown"
}

// registryString reads one string value of the registry.
func registryString(root syscall.Handle, path string, name string) (string, bool) {
	var key syscall.Handle
	if err := syscall.RegOpenKeyEx(root, syscall.StringToUTF16Ptr(path), 0,
		syscall.KEY_READ, &key); err != nil {
		return "", false
	}
	defer syscall.RegCloseKey(key)
	var size uint32
	value := syscall.StringToUTF16Ptr(name)
	if err := syscall.RegQueryValueEx(key, value, nil, nil, nil, &size); err != nil {
		return "", false
	}
	if size == 0 {
		return "", false
	}
	buffer := make([]uint16, size/2+1)
	if err := syscall.RegQueryValueEx(key, value, nil, nil,
		(*byte)(unsafe.Pointer(&buffer[0])), &size); err != nil {
		return "", false
	}
	return syscall.UTF16ToString(buffer), true
}

// totalMemory returns the physical memory of this host.
func totalMemory() string {
	type memoryStatus struct {
		length               uint32
		memoryLoad           uint32
		totalPhys            uint64
		availPhys            uint64
		totalPageFile        uint64
		availPageFile        uint64
		totalVirtual         uint64
		availVirtual         uint64
		availExtendedVirtual uint64
	}
	var status memoryStatus
	status.length = uint32(unsafe.Sizeof(status))
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 {
		return "unknown"
	}
	return formatBytes(status.totalPhys)
}
