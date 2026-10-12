package git2

import (
	"encoding/hex"
)

// Hash is a SHA-1 object id. It converts directly to go-git's
// plumbing.Hash.
type Hash [20]byte

// ZeroHash is the all-zero Hash.
var ZeroHash Hash

// NewHash parses a 40-character hex string. Invalid or short input yields
// a partially filled or zero Hash.
func NewHash(s string) Hash {
	var h Hash
	b, _ := hex.DecodeString(s)
	copy(h[:], b)
	return h
}

// IsHash reports whether s is an object id written as 40 hex digits.
func IsHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := range len(s) {
		if _, ok := unhex(s[i]); !ok {
			return false
		}
	}
	return true
}

// IsZero reports whether h is ZeroHash.
func (h Hash) IsZero() bool { return h == ZeroHash }

func (h Hash) String() string {
	var buf [40]byte
	hex.Encode(buf[:], h[:])
	return string(buf[:])
}

// parseID parses an object id written as exactly 40 hex digits.
func parseID[T string | []byte](b T) (Hash, bool) {
	if len(b) != 40 {
		return Hash{}, false
	}
	return parseHex(b)
}

// parseHex decodes the first 40 hex characters of b into a Hash.
func parseHex[T string | []byte](b T) (Hash, bool) {
	var h Hash
	if len(b) < 40 {
		return h, false
	}
	for i := range 20 {
		hi, ok1 := unhex(b[2*i])
		lo, ok2 := unhex(b[2*i+1])
		if !ok1 || !ok2 {
			return h, false
		}
		h[i] = hi<<4 | lo
	}
	return h, true
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
