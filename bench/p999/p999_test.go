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

// TestP999 builds the real collector probe and checks the documented p999
// ceilings. It is opt-in with -tags p999 because it is a long, exclusive
// measurement, not a normal gate case.
func TestP999(t *testing.T) {
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
	exe := filepath.Join(t.TempDir(), "p999")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-O2", "-I", filepath.Join(root, "include"),
		"-I", filepath.Join(root, "core", "gc", "src"), "-o", exe,
		filepath.Join(repo, "bench", "p999", "driver", "p999.c")}
	for _, name := range []string{"types.c", "region.c", "chunks.c", "heap.c",
		"object.c", "roots.c", "safepoint.c", "barrier.c", "mark.c", "evac.c",
		"cycle.c", "pacer.c"} {
		args = append(args, filepath.Join(root, "core", "gc", "src", name))
	}
	args = append(args,
		filepath.Join(root, "core", "mem", "src", "alloc.c"),
		filepath.Join(root, "platform", platform))
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
	if !strings.Contains(text, "safepoint_ns") {
		t.Fatalf("missing measurement:\n%s", text)
	}
	checkP999(t, text, "alloc_ns", 200000)
	checkP999(t, text, "safepoint_ns", 100000)
}

// checkP999 reads the p999 field of one probe line and enforces the documented
// ceiling. The probe prints nanoseconds.
func checkP999(t *testing.T, text, prefix string, ceiling int) {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		index := strings.LastIndex(line, "p999=")
		if index < 0 {
			t.Fatalf("%s has no p999: %s", prefix, line)
		}
		value, err := strconv.Atoi(strings.TrimSpace(line[index+len("p999="):]))
		if err != nil {
			t.Fatalf("%s p999: %v", prefix, err)
		}
		if value > ceiling {
			t.Fatalf("%s p999 = %dns, ceiling %dns", prefix, value, ceiling)
		}
		return
	}
	t.Fatalf("missing %s", prefix)
}
