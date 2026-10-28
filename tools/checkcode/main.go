// Command checkcode enforces the source rules of the engineering volume: no
// byte order mark, LF line endings only, ASCII-only code, and no noise
// comments. Documentation under desc/ is written in Chinese and is skipped.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Finding is one rule violation.
type Finding struct {
	Path   string
	Line   int
	Rule   string
	Detail string
}

func (f Finding) String() string {
	pos := f.Path
	if f.Line > 0 {
		pos = fmt.Sprintf("%s:%d", f.Path, f.Line)
	}
	return fmt.Sprintf("%s: %s: %s", pos, f.Rule, f.Detail)
}

// skipDirs are directories the rules do not apply to.
var skipDirs = map[string]bool{
	".git":        true,
	"build":       true,
	"desc":        true,
	".opencode":   true,
	".will-cache": true,
	// A Rust benchmark checkout keeps compiler artifacts with raw CR bytes.
	"target": true,
}

// codeExts are the file kinds that must be ASCII and carry no noise comments.
var codeExts = map[string]bool{
	".go":   true,
	".c":    true,
	".h":    true,
	".will": true,
}

// noiseWords are markers a comment must never carry.
var noiseWords = []string{
	"TODO", "FIXME", "XXX", "HACK",
	"changed in", "for now", "temporarily", "Copyright", "@author",
}

// Check walks root and returns every rule violation, sorted by path and line.
func Check(root string) ([]Finding, error) {
	var out []Finding
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		out = append(out, checkFile(rel, data)...)
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out, err
}

func checkFile(path string, data []byte) []Finding {
	var out []Finding
	hasBOM := bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if hasBOM {
		out = append(out, Finding{Path: path, Line: 1, Rule: "bom", Detail: "the file starts with a byte order mark"})
	}
	if i := bytes.IndexByte(data, '\r'); i >= 0 {
		out = append(out, Finding{Path: path, Line: lineOf(data, i), Rule: "crlf",
			Detail: "the file contains a carriage return; line endings must be LF"})
	}
	if codeExts[strings.ToLower(filepath.Ext(path))] {
		// A byte order mark is already reported, so it is not also an
		// ASCII finding.
		if !hasBOM {
			for i, b := range data {
				if b >= 0x80 {
					out = append(out, Finding{Path: path, Line: lineOf(data, i), Rule: "ascii",
						Detail: "a code file contains a byte outside ASCII"})
					break
				}
			}
		}
		out = append(out, noise(path, data)...)
	}
	return out
}

// noise reports the comment lines that carry a forbidden marker.
func noise(path string, data []byte) []Finding {
	var out []Finding
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "*") {
			continue
		}
		for _, word := range noiseWords {
			if strings.Contains(trimmed, word) {
				out = append(out, Finding{Path: path, Line: i + 1, Rule: "noise",
					Detail: fmt.Sprintf("comment carries %q", word)})
				break
			}
		}
	}
	return out
}

func lineOf(data []byte, off int) int {
	return bytes.Count(data[:off], []byte{'\n'}) + 1
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	findings, err := Check(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "checkcode: %v\n", err)
		os.Exit(2)
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stderr, f.String())
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "checkcode: %d findings\n", len(findings))
		os.Exit(1)
	}
	fmt.Println("checkcode: clean")
}
