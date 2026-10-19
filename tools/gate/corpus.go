package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/cli"
)

// corpusCase is one directory under tests/<topic>/<case>.
type corpusCase struct {
	name  string
	dir   string
	files []string
}

// runCorpus compares the recorded outputs of every corpus case under
// tests/<topic>/<case>. A case may record stdout.txt, stderr.txt, willc.txt,
// and index.txt; each recorded file must match byte for byte. With update, the
// recorded files are rewritten instead, which is how a golden moves with the
// change that alters it. The returned names are the recordings that update
// actually rewrote, relative to tests/, in case order.
func runCorpus(root string, update bool) ([]string, error) {
	cases, err := findCases(filepath.Join(root, "tests"))
	if err != nil {
		return nil, err
	}
	var failures []string
	var rewritten []string
	for _, c := range cases {
		changed, err := runCase(c, update)
		rewritten = append(rewritten, changed...)
		if err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return rewritten, fmt.Errorf("%d failing cases:\n%s", len(failures), strings.Join(failures, "\n"))
	}
	return rewritten, nil
}

// findCases lists the corpus cases in tests/, sorted by name.
func findCases(dir string) ([]corpusCase, error) {
	var out []corpusCase
	topics, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, topic := range topics {
		if !topic.IsDir() {
			continue
		}
		caseDirs, err := os.ReadDir(filepath.Join(dir, topic.Name()))
		if err != nil {
			return nil, err
		}
		for _, caseDir := range caseDirs {
			if !caseDir.IsDir() {
				continue
			}
			path := filepath.Join(dir, topic.Name(), caseDir.Name())
			files, err := caseInputs(path)
			if err != nil {
				return nil, err
			}
			if len(files) == 0 {
				continue
			}
			out = append(out, corpusCase{name: topic.Name() + "/" + caseDir.Name(), dir: path, files: files})
		}
	}
	return out, nil
}

// caseInputs lists the .will files of one case, sorted.
func caseInputs(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".will" {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// runCase compares every recorded output of one case. Every command runs from
// inside the case directory, so recorded positions are relative to the case.
// In update mode it returns the recordings it rewrote.
func runCase(c corpusCase, update bool) ([]string, error) {
	var rewritten []string
	target := "."
	if _, err := os.Stat(filepath.Join(c.dir, "will.mod")); err != nil && len(c.files) == 1 {
		target = filepath.Base(c.files[0])
	}

	if _, ok := recorded(c.dir, "stdout.txt"); ok {
		code, got, _, err := inDir(c.dir, "check", target)
		if err != nil {
			return rewritten, fmt.Errorf("%s: %v", c.name, err)
		}
		if update {
			changed, err := updateRecording(c.dir, "stdout.txt", got)
			if err != nil {
				return rewritten, err
			}
			if changed {
				rewritten = append(rewritten, c.name+"/stdout.txt")
			}
		} else {
			want, _ := recorded(c.dir, "stdout.txt")
			if !bytes.Equal(got, want) {
				return rewritten, mismatch(c.name, "stdout.txt", want, got)
			}
			if wantCode := expectedCode(want); code != wantCode {
				return rewritten, fmt.Errorf("%s: exit code = %d, want %d", c.name, code, wantCode)
			}
		}
	}
	if _, ok := recorded(c.dir, "stderr.txt"); ok {
		_, _, got, err := inDir(c.dir, "check", target)
		if err != nil {
			return rewritten, fmt.Errorf("%s: %v", c.name, err)
		}
		if update {
			changed, err := updateRecording(c.dir, "stderr.txt", got)
			if err != nil {
				return rewritten, err
			}
			if changed {
				rewritten = append(rewritten, c.name+"/stderr.txt")
			}
		} else {
			want, _ := recorded(c.dir, "stderr.txt")
			if !bytes.Equal(got, want) {
				return rewritten, mismatch(c.name, "stderr.txt", want, got)
			}
		}
	}
	if _, ok := recorded(c.dir, "willc.txt"); ok {
		if len(c.files) != 1 {
			return rewritten, fmt.Errorf("%s: willc.txt needs exactly one .will input", c.name)
		}
		code, got, _, err := inDir(c.dir, "emit-willc", filepath.Base(c.files[0]))
		if err := commandOK(c.name, "emit-willc", code, err); err != nil {
			return rewritten, err
		}
		if update {
			changed, err := updateRecording(c.dir, "willc.txt", got)
			if err != nil {
				return rewritten, err
			}
			if changed {
				rewritten = append(rewritten, c.name+"/willc.txt")
			}
		} else {
			want, _ := recorded(c.dir, "willc.txt")
			if !bytes.Equal(got, want) {
				return rewritten, mismatch(c.name, "willc.txt", want, got)
			}
		}
	}
	// A case with several files records one willc per file, named after it.
	for _, f := range c.files {
		name := strings.TrimSuffix(filepath.Base(f), ".will") + ".willc.txt"
		if _, ok := recorded(c.dir, name); !ok {
			continue
		}
		code, got, _, err := inDir(c.dir, "emit-willc", filepath.Base(f))
		if err := commandOK(c.name, "emit-willc", code, err); err != nil {
			return rewritten, err
		}
		if update {
			changed, err := updateRecording(c.dir, name, got)
			if err != nil {
				return rewritten, err
			}
			if changed {
				rewritten = append(rewritten, c.name+"/"+name)
			}
			continue
		}
		want, _ := recorded(c.dir, name)
		if !bytes.Equal(got, want) {
			return rewritten, mismatch(c.name, name, want, got)
		}
	}
	if _, ok := recorded(c.dir, "llvm.txt"); ok {
		code, got, _, err := inDir(c.dir, "emit-llvm", target)
		if err := commandOK(c.name, "emit-llvm", code, err); err != nil {
			return rewritten, err
		}
		if update {
			changed, err := updateRecording(c.dir, "llvm.txt", got)
			if err != nil {
				return rewritten, err
			}
			if changed {
				rewritten = append(rewritten, c.name+"/llvm.txt")
			}
		} else {
			want, _ := recorded(c.dir, "llvm.txt")
			if !bytes.Equal(got, want) {
				return rewritten, mismatch(c.name, "llvm.txt", want, got)
			}
		}
	}
	if _, ok := recorded(c.dir, "index.txt"); ok {
		code, got, _, err := inDir(c.dir, "index", ".")
		if err := commandOK(c.name, "index", code, err); err != nil {
			return rewritten, err
		}
		if update {
			changed, err := updateRecording(c.dir, "index.txt", got)
			if err != nil {
				return rewritten, err
			}
			if changed {
				rewritten = append(rewritten, c.name+"/index.txt")
			}
		} else {
			want, _ := recorded(c.dir, "index.txt")
			if !bytes.Equal(got, want) {
				return rewritten, mismatch(c.name, "index.txt", want, got)
			}
		}
	}
	if _, ok := recorded(c.dir, "decisions.txt"); ok {
		_, got, _, err := inDir(c.dir, "emit-decisions", target)
		if err != nil {
			return rewritten, fmt.Errorf("%s: %v", c.name, err)
		}
		if update {
			changed, err := updateRecording(c.dir, "decisions.txt", got)
			if err != nil {
				return rewritten, err
			}
			if changed {
				rewritten = append(rewritten, c.name+"/decisions.txt")
			}
		} else {
			want, _ := recorded(c.dir, "decisions.txt")
			if !bytes.Equal(got, want) {
				return rewritten, mismatch(c.name, "decisions.txt", want, got)
			}
		}
	}
	return rewritten, nil
}

// updateRecording rewrites one recording only when its bytes changed, and
// reports whether it did.
func updateRecording(dir, name string, got []byte) (bool, error) {
	want, _ := recorded(dir, name)
	if bytes.Equal(got, want) {
		return false, nil
	}
	if err := writeRecording(dir, name, got); err != nil {
		return false, err
	}
	return true, nil
}

// writeRecording replaces one recorded output.
func writeRecording(dir, name string, data []byte) error {
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return fmt.Errorf("%s: %v", name, err)
	}
	return nil
}

// inDir runs one will command from inside dir and returns its exit code and
// streams.
func inDir(dir string, args ...string) (int, []byte, []byte, error) {
	old, err := os.Getwd()
	if err != nil {
		return 0, nil, nil, err
	}
	if err := os.Chdir(dir); err != nil {
		return 0, nil, nil, err
	}
	defer os.Chdir(old)
	var out, errBuf bytes.Buffer
	code := cli.Run(args, &out, &errBuf)
	return code, out.Bytes(), errBuf.Bytes(), nil
}

// recorded reads one recorded output file. A missing file is simply absent.
func recorded(dir, name string) ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	return data, err == nil
}

// expectedCode returns the exit code a recorded stdout implies: diagnostics
// mean 1, an empty recording means 0.
func expectedCode(want []byte) int {
	if len(want) == 0 {
		return 0
	}
	return 1
}

// commandOK reports a corpus command that failed to run or exited nonzero. A
// recording must never capture a diagnostic in place of the promised output.
func commandOK(caseName, command string, code int, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %v", caseName, err)
	}
	if code != 0 {
		return fmt.Errorf("%s: %s exited with %d", caseName, command, code)
	}
	return nil
}

func mismatch(name, file string, want, got []byte) error {
	return fmt.Errorf("%s: %s differs\n--- want\n%s--- got\n%s", name, file, want, got)
}
