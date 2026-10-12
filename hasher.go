package git2

import (
	"crypto/sha1"
	"hash"
	"strconv"
)

// Hasher computes object ids. Write the object's contents, then call Sum.
type Hasher struct{ hash.Hash }

// NewHasher returns a Hasher for an object of type t and the given size.
func NewHasher(t ObjectType, size int64) Hasher {
	h := Hasher{sha1.New()}
	var hdr [32]byte
	b := append(hdr[:0], t.String()...)
	b = append(b, ' ')
	b = strconv.AppendInt(b, size, 10)
	b = append(b, 0)
	h.Write(b)
	return h
}

// Sum returns the object id.
func (h Hasher) Sum() (id Hash) {
	h.Hash.Sum(id[:0])
	return id
}

// ComputeHash returns the id of an object of type t with the given
// contents.
func ComputeHash(t ObjectType, content []byte) Hash {
	h := NewHasher(t, int64(len(content)))
	h.Write(content)
	return h.Sum()
}
