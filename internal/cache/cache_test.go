package cache

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKeyIsDeterministicAndSensitive(t *testing.T) {
	a := NewHasher()
	a.Add("source", []byte("func F() {}"))
	a.Add("compiler", []byte("will dev"))
	first := a.Key()

	b := NewHasher()
	b.Add("source", []byte("func F() {}"))
	b.Add("compiler", []byte("will dev"))
	if first != b.Key() {
		t.Fatalf("same inputs gave different keys: %s %s", first, b.Key())
	}

	c := NewHasher()
	c.Add("source", []byte("func G() {}"))
	c.Add("compiler", []byte("will dev"))
	if first == c.Key() {
		t.Fatalf("a changed source kept the same key")
	}

	d := NewHasher()
	d.Add("compiler", []byte("will dev"))
	d.Add("source", []byte("func F() {}"))
	if first == d.Key() {
		t.Fatalf("part order did not change the key")
	}
}

func TestKeyDelimitsParts(t *testing.T) {
	a := NewHasher()
	a.Add("ab", []byte("c"))
	b := NewHasher()
	b.Add("a", []byte("bc"))
	if a.Key() == b.Key() {
		t.Fatalf("labels are not delimited: %s", a.Key())
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := New(t.TempDir(), 0)
	key := "deadbeef"
	if _, ok := s.Get(key); ok {
		t.Fatal("empty store reported a hit")
	}
	want := map[string][]byte{Willc: []byte("front end"), Decisions: []byte("decisions")}
	if err := s.Put(key, want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := s.Get(key, Willc, Decisions)
	if !ok {
		t.Fatal("entry was not found after put")
	}
	if !bytes.Equal(got[Willc], want[Willc]) || !bytes.Equal(got[Decisions], want[Decisions]) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, ok := s.Get(key, ModuleO); ok {
		t.Fatal("missing entry reported a hit")
	}
}

func TestStoreRejectsUnknownEntry(t *testing.T) {
	s := New(t.TempDir(), 0)
	if err := s.Put("k", map[string][]byte{"../escape": []byte("x")}); err == nil {
		t.Fatal("an unknown entry name was accepted")
	}
}

func TestDiagOnlyEntryHits(t *testing.T) {
	s := New(t.TempDir(), 0)
	if err := s.Put("k", map[string][]byte{Diag: []byte("T0001 type mismatch")}); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := s.Get("k", Diag)
	if !ok || string(got[Diag]) != "T0001 type mismatch" {
		t.Fatalf("diag-only entry did not hit: %v %v", got, ok)
	}
}

func TestTrimIfNeededSkipsRecentScans(t *testing.T) {
	root := t.TempDir()
	s := New(root, 8)
	for _, key := range []string{"old", "new"} {
		if err := s.Put(key, map[string][]byte{Willc: []byte("12345678")}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	if err := s.TrimIfNeeded(); err != nil {
		t.Fatalf("first trim: %v", err)
	}
	if err := s.Put("later", map[string][]byte{Willc: []byte("12345678")}); err != nil {
		t.Fatalf("put later: %v", err)
	}
	if err := s.TrimIfNeeded(); err != nil {
		t.Fatalf("second trim: %v", err)
	}
	if !s.Has("later") {
		t.Fatal("TrimIfNeeded scanned again immediately and removed the new entry")
	}
}

func TestTrimDropsLeastRecentlyUsed(t *testing.T) {
	root := t.TempDir()
	s := New(root, 8)
	base := time.Now().Add(-time.Hour)
	for i, key := range []string{"old", "mid", "new"} {
		if err := s.Put(key, map[string][]byte{Willc: []byte("01234567")}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
		when := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(filepath.Join(root, key), when, when); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	if err := s.Trim(); err != nil {
		t.Fatalf("trim: %v", err)
	}
	if s.Has("old") {
		t.Error("the least recently used entry survived")
	}
	if !s.Has("new") {
		t.Error("the newest entry was dropped")
	}
}
