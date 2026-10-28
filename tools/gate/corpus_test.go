package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mismatchSource fails the type checker, so its stdout recording is a
// diagnostic and the check exits 1.
const mismatchSource = "def f() {\n    var n: i32 = \"text\"\n    _ = n\n}\n"

func TestRunCorpusUpdateRewritesChangedRecording(t *testing.T) {
	root, caseDir := newCorpusCase(t)
	recording := filepath.Join(caseDir, "stdout.txt")
	stale := []byte("stale recording\n")
	writeRecordingFile(t, recording, stale)

	rewritten, err := runCorpus(root, true)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	want := []string{"check/type-mismatch/stdout.txt"}
	if len(rewritten) != 1 || rewritten[0] != want[0] {
		t.Fatalf("rewritten = %v, want %v", rewritten, want)
	}

	var out bytes.Buffer
	if code := reportRewrites(&out, rewritten); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	wantOut := "gate: rewrote check/type-mismatch/stdout.txt\n" +
		"gate: recordings changed; run again without -update to confirm\n"
	if out.String() != wantOut {
		t.Fatalf("output = %q, want %q", out.String(), wantOut)
	}

	_, wantStdout, _, err := inDir(caseDir, "check", "bad.will")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	got, err := os.ReadFile(recording)
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	if !bytes.Equal(got, wantStdout) {
		t.Fatalf("recording = %q, want %q", got, wantStdout)
	}
	if !strings.Contains(string(got), "T0001 type mismatch") {
		t.Fatalf("recording = %q, want a T0001 diagnostic", got)
	}
}

func TestRunCorpusUpdateIsCleanWhenRecordingsMatch(t *testing.T) {
	root, caseDir := newCorpusCase(t)
	_, wantStdout, _, err := inDir(caseDir, "check", "bad.will")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	writeRecordingFile(t, filepath.Join(caseDir, "stdout.txt"), wantStdout)

	rewritten, err := runCorpus(root, true)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(rewritten) != 0 {
		t.Fatalf("rewritten = %v, want none", rewritten)
	}
	var out bytes.Buffer
	if code := reportRewrites(&out, rewritten); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q, want empty", out.String())
	}
}

func TestReportRewritesPrintsEachRecording(t *testing.T) {
	var out bytes.Buffer
	code := reportRewrites(&out, []string{"build/loops/llvm.txt", "check/bad/stdout.txt"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	want := "gate: rewrote build/loops/llvm.txt\n" +
		"gate: rewrote check/bad/stdout.txt\n" +
		"gate: recordings changed; run again without -update to confirm\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestRunCorpusWithoutUpdateRejectsMismatch(t *testing.T) {
	root, caseDir := newCorpusCase(t)
	writeRecordingFile(t, filepath.Join(caseDir, "stdout.txt"), []byte("stale recording\n"))

	if _, err := runCorpus(root, false); err == nil {
		t.Fatal("runCorpus accepted a mismatched recording")
	}
}

// newCorpusCase creates one standalone corpus case under a temporary root.
func newCorpusCase(t *testing.T) (root, caseDir string) {
	t.Helper()
	root = t.TempDir()
	caseDir = filepath.Join(root, "tests", "check", "type-mismatch")
	write(t, root, filepath.Join("tests", "check", "type-mismatch", "bad.will"), []byte(mismatchSource))
	return root, caseDir
}

func writeRecordingFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
}
