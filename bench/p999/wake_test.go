//go:build p999

package p999

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestWakeP999 checks the parked-task wake ceiling on one host. It compiles the
// task and channel engines with the real collector and measures the send-to-join
// interval; it is separate from the allocation probe because the two paths have
// different p999 budgets.
func TestWakeP999(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is not on PATH")
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	root := filepath.Join(repo, "runtime")
	platform := "win.c"
	switch runtime.GOOS {
	case "linux":
		platform = "linux.c"
	case "darwin":
		platform = "mac.c"
	case "android":
		platform = "android.c"
	case "ios":
		platform = "ios.c"
	}
	exe := filepath.Join(t.TempDir(), "wake")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-O2", "-Wall", "-Wextra", "-Werror",
		"-I", filepath.Join(root, "include"),
		"-I", filepath.Join(root, "core", "gc", "src"),
		"-o", exe,
		filepath.Join(repo, "bench", "p999", "driver", "wake.c"),
		filepath.Join(root, "core", "task", "src", "task.c"),
		filepath.Join(root, "core", "chan", "src", "chan.c"),
		filepath.Join(root, "core", "mem", "src", "alloc.c"),
		filepath.Join(root, "core", "report", "src", "report.c"),
		filepath.Join(root, "core", "startup", "src", "startup.c"),
		filepath.Join(root, "core", "timer", "src", "timer.c"),
		filepath.Join(root, "core", "blocking", "src", "blocking.c"),
		filepath.Join(root, "core", "poll", "src", "poll.c"),
		filepath.Join(root, "platform", platform)}
	for _, name := range []string{"types.c", "region.c", "chunks.c", "heap.c",
		"object.c", "roots.c", "safepoint.c", "barrier.c", "mark.c", "evac.c",
		"cycle.c", "pacer.c"} {
		args = append(args, filepath.Join(root, "core", "gc", "src", name))
	}
	if runtime.GOOS == "windows" {
		args = append(args, "-static", "-lws2_32")
	} else {
		args = append(args, "-pthread")
	}
	if out, err := exec.Command(gcc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	text := string(out)
	t.Logf("%s", text)
	checkWakeP999(t, text, 50000)
}

func checkWakeP999(t *testing.T, text string, ceiling int) {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "wake_ns") {
			continue
		}
		index := strings.LastIndex(line, "p999=")
		if index < 0 {
			t.Fatalf("wake line has no p999: %s", line)
		}
		value, err := strconv.Atoi(strings.TrimSpace(line[index+len("p999="):]))
		if err != nil {
			t.Fatalf("wake p999: %v", err)
		}
		if value > ceiling {
			t.Fatalf("wake p999 = %dns, ceiling %dns", value, ceiling)
		}
		return
	}
	t.Fatalf("missing wake measurement:\n%s", text)
}
