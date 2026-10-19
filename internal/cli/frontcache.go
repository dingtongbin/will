package cli

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/pkg"
)

// frontEndStats counts the package lookups of one process so a test can tell a
// hit from a miss without watching the clock or the disk.
var frontEndStats struct {
	hits   int
	misses int
}

// frontStore returns the store for front-end products, or nil when the cache
// directory cannot be resolved. The environment is validated before a command
// runs, so an error here only means the command runs uncached.
func frontStore() *cache.Store {
	root, err := cache.Root()
	if err != nil {
		return nil
	}
	limit, _ := cache.Limit()
	return cache.New(root, limit)
}

// frontKey returns the key of one package's front-end products and whether the
// package can be cached. The key covers the compiler identity, the flags that
// can change the text, the package's own files, and the product of every direct
// import, so a change anywhere in the interface reaches the key.
func frontKey(p *pkg.Package, imports map[string][]byte, aliases map[string]string, opts buildOptions) (string, bool) {
	h := cache.NewHasher()
	h.AddStrings("compiler", compilerIdentity(), opts.target, opts.features,
		fmt.Sprintf("freestanding=%v", opts.freestanding))
	h.AddStrings("package", p.Path)
	for _, f := range p.Files {
		if f.AST == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.Root, f.Path))
		if err != nil {
			return "", false
		}
		h.Add(f.Path, data)
	}
	paths := map[string]bool{}
	for _, f := range p.Files {
		if f.AST == nil {
			continue
		}
		for _, imp := range f.AST.Imports {
			paths[imp.Path] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		key := path
		if canonical := aliases[path]; canonical != "" {
			key = canonical
		}
		blob, ok := imports[key]
		if !ok {
			// An import outside this build (a C header or a package that is not
			// part of the graph) leaves the package uncached rather than keyed
			// on a guess.
			if isCHeaderImport(p, path) {
				continue
			}
			return "", false
		}
		h.Add("import:"+path, blob)
	}
	return h.Key(), true
}

// isCHeaderImport reports whether path is written in an import c block. Those
// imports name a header, not a package of the graph.
func isCHeaderImport(p *pkg.Package, path string) bool {
	for _, f := range p.Files {
		if f.AST == nil {
			continue
		}
		for _, imp := range f.AST.Imports {
			if imp.C && imp.Path == path {
				return true
			}
		}
	}
	return false
}

// encodeWillc packs the per-file texts of one package. The paths and texts are
// length-delimited in file order, so the same package always encodes the same
// bytes.
func encodeWillc(paths, texts []string) []byte {
	var b bytes.Buffer
	var buf [binary.MaxVarintLen64]byte
	put := func(n int) {
		written := binary.PutUvarint(buf[:], uint64(n))
		b.Write(buf[:written])
	}
	put(len(paths))
	for i := range paths {
		put(len(paths[i]))
		b.WriteString(paths[i])
		put(len(texts[i]))
		b.WriteString(texts[i])
	}
	return b.Bytes()
}

// decodeWillc reads what encodeWillc wrote.
func decodeWillc(data []byte) (paths, texts []string, ok bool) {
	r := bytes.NewReader(data)
	read := func() (int, bool) {
		n, err := binary.ReadUvarint(r)
		if err != nil || n > uint64(len(data)) {
			return 0, false
		}
		return int(n), true
	}
	count, ok := read()
	if !ok || count > len(data) {
		return nil, nil, false
	}
	paths = make([]string, count)
	texts = make([]string, count)
	for i := 0; i < count; i++ {
		n, ok := read()
		if !ok || n > r.Len() {
			return nil, nil, false
		}
		name := make([]byte, n)
		if _, err := io.ReadFull(r, name); err != nil {
			return nil, nil, false
		}
		n, ok = read()
		if !ok || n > r.Len() {
			return nil, nil, false
		}
		text := make([]byte, n)
		if _, err := io.ReadFull(r, text); err != nil {
			return nil, nil, false
		}
		paths[i] = string(name)
		texts[i] = string(text)
	}
	if r.Len() != 0 {
		return nil, nil, false
	}
	return paths, texts, true
}
