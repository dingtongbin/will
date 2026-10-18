// Package cache implements the three-level cache and its keys. The key must
// contain everything the stage really read, plus the compiler identity.
package cache

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strconv"
)

// Hasher builds one cache key. Every part is length-delimited, so two
// different sequences of parts can never hash the same bytes.
type Hasher struct {
	h hash.Hash
}

// NewHasher starts a key.
func NewHasher() *Hasher {
	return &Hasher{h: sha256.New()}
}

// Add mixes one labeled part.
func (h *Hasher) Add(label string, data []byte) {
	h.addLen(uint64(len(label)))
	h.h.Write([]byte(label))
	h.addLen(uint64(len(data)))
	h.h.Write(data)
}

// AddStrings mixes one labeled part built from text values.
func (h *Hasher) AddStrings(label string, values ...string) {
	sub := NewHasher()
	for _, v := range values {
		sub.Add(strconv.Itoa(len(v)), []byte(v))
	}
	h.Add(label, []byte(sub.Key()))
}

func (h *Hasher) addLen(n uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], n)
	h.h.Write(buf[:])
}

// Key returns the hexadecimal digest.
func (h *Hasher) Key() string {
	return hex.EncodeToString(h.h.Sum(nil))
}
