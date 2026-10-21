package cache

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// trimInterval bounds how often a build scans the cache for LRU eviction.
const trimInterval = 10 * time.Minute

// Entry names fixed by the build volume.
const (
	// Willc is the front-end product.
	Willc = "willc"
	// Decisions is the decision record.
	Decisions = "decisions"
	// ModuleLL is the lowered LLVM text.
	ModuleLL = "module.ll"
	// ModuleO is the object file.
	ModuleO = "module.o"
	// Diag holds diagnostics when no product was produced.
	Diag = "diag"
	// Artifact is the final linked executable or library.
	Artifact = "artifact"
)

var entryNames = map[string]bool{
	Willc: true, Decisions: true, ModuleLL: true, ModuleO: true, Diag: true,
	Artifact: true,
}

// Store is a cache rooted at one directory.
type Store struct {
	Root string
	Max  int64
}

// New returns a store rooted at root. A non-positive max disables trimming.
func New(root string, max int64) *Store {
	return &Store{Root: root, Max: max}
}

// Put writes one entry. An entry holds only fixed names.
func (s *Store) Put(key string, entries map[string][]byte) error {
	dir := filepath.Join(s.Root, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, data := range entries {
		if !entryNames[name] {
			return &os.PathError{Op: "cache", Path: name, Err: os.ErrInvalid}
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	touch(dir)
	return nil
}

// Get reads the requested entry names. It reports false when any is missing,
// which is also how a cache miss looks.
func (s *Store) Get(key string, names ...string) (map[string][]byte, bool) {
	if len(names) == 0 {
		names = []string{Willc}
	}
	dir := filepath.Join(s.Root, key)
	out := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, false
		}
		out[name] = data
	}
	touch(dir)
	return out, true
}

// Has reports whether the entry exists.
func (s *Store) Has(key string) bool {
	info, err := os.Stat(filepath.Join(s.Root, key))
	return err == nil && info.IsDir()
}

// TrimIfNeeded trims at most once per trimInterval. A build writes several
// stores, and scanning a large cache on every store write made a warm build
// spend seconds in bookkeeping. The marker is a file, so Trim ignores it.
func (s *Store) TrimIfNeeded() error {
	if s.Max <= 0 {
		return nil
	}
	marker := filepath.Join(filepath.Dir(s.Root), ".trim-"+filepath.Base(s.Root))
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < trimInterval {
		return nil
	}
	if err := s.Trim(); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte("trimmed\n"), 0o644)
}

// Trim removes least-recently-used entries until the cache fits its limit.
func (s *Store) Trim() error {
	if s.Max <= 0 {
		return nil
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var list []entry
	var total int64
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		path := filepath.Join(s.Root, d.Name())
		size, mod := dirSize(path)
		list = append(list, entry{path: path, size: size, mod: mod})
		total += size
	}
	if total <= s.Max {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod.Before(list[j].mod) })
	for _, x := range list {
		if total <= s.Max {
			break
		}
		if err := os.RemoveAll(x.path); err != nil {
			return err
		}
		total -= x.size
	}
	return nil
}

func dirSize(dir string) (int64, time.Time) {
	var total int64
	var mod time.Time
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, time.Time{}
	}
	for _, d := range entries {
		if d.IsDir() {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		if info.ModTime().After(mod) {
			mod = info.ModTime()
		}
	}
	return total, mod
}

func touch(dir string) {
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}
