package cache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// TestStoreRoundTripAcrossNames pins that one key holds an independent set of
// named entries and that a key that was never written is a miss.
func TestStoreRoundTripAcrossNames(t *testing.T) {
	s := New(t.TempDir(), 0)
	want := map[string][]byte{
		Willc:     []byte("front end"),
		Decisions: []byte("decisions"),
		ModuleLL:  []byte("module.ll"),
		ModuleO:   {0x00, 0x01, 0xff},
		Diag:      []byte("T0001 type mismatch"),
	}
	if err := s.Put("key-one", want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := s.Get("key-one", Willc, Decisions, ModuleLL, ModuleO, Diag)
	if !ok {
		t.Fatal("the entry set was not found after put")
	}
	for name, data := range want {
		if !bytes.Equal(got[name], data) {
			t.Errorf("%s = %q, want %q", name, got[name], data)
		}
	}
	if _, ok := s.Get("key-two"); ok {
		t.Fatal("a key that was never written reported a hit")
	}
	if !s.Has("key-one") {
		t.Fatal("Has reported false for a written key")
	}
	if s.Has("key-two") {
		t.Fatal("Has reported true for a key that was never written")
	}
}

// TestStoreDefaultsToWillc pins that Get with no names reads the willc entry.
func TestStoreDefaultsToWillc(t *testing.T) {
	s := New(t.TempDir(), 0)
	if err := s.Put("k", map[string][]byte{Willc: []byte("front end")}); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := s.Get("k")
	if !ok || string(got[Willc]) != "front end" {
		t.Fatalf("default Get = %q, %v", got, ok)
	}
}

// TestStoreKeyIsolation pins that two keys never share entry bytes.
func TestStoreKeyIsolation(t *testing.T) {
	s := New(t.TempDir(), 0)
	if err := s.Put("alpha", map[string][]byte{Willc: []byte("one")}); err != nil {
		t.Fatalf("put alpha: %v", err)
	}
	if err := s.Put("beta", map[string][]byte{Willc: []byte("two")}); err != nil {
		t.Fatalf("put beta: %v", err)
	}
	for key, want := range map[string]string{"alpha": "one", "beta": "two"} {
		got, ok := s.Get(key, Willc)
		if !ok {
			t.Fatalf("%s was not found", key)
		}
		if string(got[Willc]) != want {
			t.Fatalf("%s = %q, want %q", key, got[Willc], want)
		}
	}
	if _, ok := s.Get("gamma", Willc); ok {
		t.Fatal("an unknown key reported a hit")
	}
}

// TestStoreCorruptEntryDoesNotPanic pins the current behavior of Get on a
// damaged entry. Get does not validate entry contents: a truncated file and a
// file holding garbage are hits that return those exact bytes. A missing
// entry, an entry replaced by a directory, and a key path that is a regular
// file are misses. None of them panics. Correctness after corruption rests on
// the key, not on the store rejecting bytes.
func TestStoreCorruptEntryDoesNotPanic(t *testing.T) {
	root := t.TempDir()
	s := New(root, 0)
	const key = "corrupt"
	if err := s.Put(key, map[string][]byte{Willc: []byte("front end text")}); err != nil {
		t.Fatalf("put: %v", err)
	}
	path := filepath.Join(root, key, Willc)

	if err := os.Truncate(path, 3); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	got, ok, recovered := getRecover(s, key, Willc)
	if recovered != nil {
		t.Fatalf("Get panicked on a truncated entry: %v", recovered)
	}
	if !ok || string(got[Willc]) != "fro" {
		t.Fatalf("truncated entry = %q, %v; want the raw bytes and a hit", got, ok)
	}

	if err := os.WriteFile(path, []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	got, ok, recovered = getRecover(s, key, Willc)
	if recovered != nil {
		t.Fatalf("Get panicked on a garbage entry: %v", recovered)
	}
	if !ok || string(got[Willc]) != "garbage" {
		t.Fatalf("garbage entry = %q, %v; want the raw bytes and a hit", got, ok)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}
	_, ok, recovered = getRecover(s, key, Willc)
	if recovered != nil {
		t.Fatalf("Get panicked on a directory entry: %v", recovered)
	}
	if ok {
		t.Fatal("a directory where an entry file belongs reported a hit")
	}

	keyFile := filepath.Join(root, "key-is-a-file")
	if err := os.WriteFile(keyFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	_, ok, recovered = getRecover(s, "key-is-a-file", Willc)
	if recovered != nil {
		t.Fatalf("Get panicked when the key path was a file: %v", recovered)
	}
	if ok {
		t.Fatal("a key path that is a regular file reported a hit")
	}
}

// getRecover runs Get and reports a panic instead of letting it escape, so the
// test states the no-panic contract explicitly.
func getRecover(s *Store, key string, names ...string) (map[string][]byte, bool, any) {
	var got map[string][]byte
	var ok bool
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		got, ok = s.Get(key, names...)
	}()
	return got, ok, recovered
}

// TestStoreMissingNameIsMiss pins that a multi-name read is all or nothing.
func TestStoreMissingNameIsMiss(t *testing.T) {
	s := New(t.TempDir(), 0)
	if err := s.Put("k", map[string][]byte{Willc: []byte("front end")}); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := s.Get("k", Willc, ModuleO)
	if ok {
		t.Fatal("a partially present entry set reported a hit")
	}
	if got != nil {
		t.Fatalf("a miss returned a partial map: %v", got)
	}
}

// TestStoreConcurrentGetPut runs eight workers over independent keys. The test
// fails if a worker crashes or reads another worker's bytes.
func TestStoreConcurrentGetPut(t *testing.T) {
	const workers = 8
	const rounds = 20

	s := New(t.TempDir(), 0)
	failures := make(chan string, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%02d", w)
			willc := bytes.Repeat([]byte{byte('a' + w)}, 512+w)
			diagData := []byte(fmt.Sprintf("worker-%d", w))
			entries := map[string][]byte{Willc: willc, Diag: diagData}
			for r := 0; r < rounds; r++ {
				if err := s.Put(key, entries); err != nil {
					failures <- fmt.Sprintf("worker %d put: %v", w, err)
					return
				}
				got, ok := s.Get(key, Willc, Diag)
				if !ok {
					failures <- fmt.Sprintf("worker %d got a miss after its own put", w)
					return
				}
				if !bytes.Equal(got[Willc], willc) {
					failures <- fmt.Sprintf("worker %d read another worker's willc bytes", w)
					return
				}
				if !bytes.Equal(got[Diag], diagData) {
					failures <- fmt.Sprintf("worker %d read another worker's diag bytes", w)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}

// TestTrimIsDeterministic pins the LRU cutoff: with five equal-sized entries
// and a limit that holds three, the two oldest go and repeated runs keep the
// same set. It also pins that a second Trim on the same store changes nothing.
func TestTrimIsDeterministic(t *testing.T) {
	keys := []string{"entry-a", "entry-b", "entry-c", "entry-d", "entry-e"}
	const max int64 = 24

	run := func(t *testing.T) []string {
		t.Helper()
		s := New(t.TempDir(), max)
		base := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
		for i, key := range keys {
			data := bytes.Repeat([]byte{byte('a' + i)}, 8)
			if err := s.Put(key, map[string][]byte{Willc: data}); err != nil {
				t.Fatalf("put %s: %v", key, err)
			}
			when := base.Add(time.Duration(i) * time.Minute)
			if err := os.Chtimes(filepath.Join(s.Root, key, Willc), when, when); err != nil {
				t.Fatalf("chtimes %s: %v", key, err)
			}
		}
		if err := s.Trim(); err != nil {
			t.Fatalf("trim: %v", err)
		}
		if err := s.Trim(); err != nil {
			t.Fatalf("second trim: %v", err)
		}
		var kept []string
		for _, key := range keys {
			if s.Has(key) {
				kept = append(kept, key)
			}
		}
		return kept
	}

	first := run(t)
	second := run(t)
	if !slices.Equal(first, second) {
		t.Fatalf("two identical runs kept %v and %v", first, second)
	}
	if want := keys[2:]; !slices.Equal(first, want) {
		t.Fatalf("kept %v, want %v", first, want)
	}
}

// TestTrimWithEqualTimestampsKeepsOneDeterministicSet pins that tied
// timestamps still yield one retained set for one input and one limit.
func TestTrimWithEqualTimestampsKeepsOneDeterministicSet(t *testing.T) {
	keys := []string{"entry-a", "entry-b", "entry-c", "entry-d", "entry-e"}
	const max int64 = 16

	run := func(t *testing.T) []string {
		t.Helper()
		s := New(t.TempDir(), max)
		when := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
		for i, key := range keys {
			data := bytes.Repeat([]byte{byte('a' + i)}, 8)
			if err := s.Put(key, map[string][]byte{Willc: data}); err != nil {
				t.Fatalf("put %s: %v", key, err)
			}
			if err := os.Chtimes(filepath.Join(s.Root, key, Willc), when, when); err != nil {
				t.Fatalf("chtimes %s: %v", key, err)
			}
		}
		if err := s.Trim(); err != nil {
			t.Fatalf("trim: %v", err)
		}
		var kept []string
		for _, key := range keys {
			if s.Has(key) {
				kept = append(kept, key)
			}
		}
		return kept
	}

	first := run(t)
	second := run(t)
	if !slices.Equal(first, second) {
		t.Fatalf("two identical runs kept %v and %v", first, second)
	}
	if len(first) != 2 {
		t.Fatalf("kept %d entries, want 2: %v", len(first), first)
	}
}

// TestTrimEmptyStore pins that trimming an empty or missing store is safe and
// that a non-positive limit disables trimming.
func TestTrimEmptyStore(t *testing.T) {
	if err := New(t.TempDir(), 16).Trim(); err != nil {
		t.Fatalf("trim empty store: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := New(missing, 16).Trim(); err != nil {
		t.Fatalf("trim missing store: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("trim created the missing store root: %v", err)
	}

	s := New(t.TempDir(), 0)
	if err := s.Put("kept", map[string][]byte{Willc: bytes.Repeat([]byte("x"), 100)}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.Trim(); err != nil {
		t.Fatalf("trim with a non-positive limit: %v", err)
	}
	if !s.Has("kept") {
		t.Fatal("a non-positive limit dropped an entry")
	}
}
