package git2

import (
	"fmt"
	"strings"
)

// Tag is an annotated tag object.
type Tag struct {
	Hash         Hash
	Name         string
	Tagger       Signature
	Message      string
	PGPSignature string
	TargetType   ObjectType
	Target       Hash

	r *Repository
}

// decode parses a raw tag. The first three lines must be the object, type
// and tag headers, in that order, and the tagger is read only from the line
// after them. Strings alias buf.
func (t *Tag) decode(buf string) error {
	const (
		atObject = iota
		atType
		atName
		atTagger
		inHeaders
	)
	missing := [...]string{"object", "type", "tag"}
	pos := atObject
	b := buf
	for len(b) > 0 {
		line, rest := cutLine(b)
		terminated := len(line) < len(b)
		b = rest
		if len(line) == 0 {
			break // the blank line before the message
		}
		key, val, _ := strings.Cut(line, " ")
		switch pos {
		case atObject:
			if key != "object" {
				return fmt.Errorf("%w: object header must be first", ErrMalformedTag)
			}
			h, ok := parseID(val)
			if !ok {
				return fmt.Errorf("%w: bad object hash", ErrMalformedTag)
			}
			t.Target = h
		case atType:
			if key != "type" {
				return fmt.Errorf("%w: type header must follow object", ErrMalformedTag)
			}
			if t.TargetType = parseObjectType(val); t.TargetType == InvalidObject {
				return ErrInvalidType
			}
		case atName:
			if key != "tag" {
				return fmt.Errorf("%w: tag header must follow type", ErrMalformedTag)
			}
			t.Name = val
		case atTagger:
			if key == "tagger" {
				t.Tagger.decode(val)
			}
		}
		// Other headers, and their continuation lines, are not kept.
		pos = min(pos+1, inHeaders)
		if !terminated {
			// A tag that ends without a newline after any header is
			// accepted, even before the type or name.
			return nil
		}
	}
	if pos < atTagger {
		return fmt.Errorf("%w: missing %s header", ErrMalformedTag, missing[pos])
	}
	msg := b
	if i := signatureStart(msg); i >= 0 {
		t.PGPSignature = msg[i:]
		msg = msg[:i]
	}
	t.Message = msg
	return nil
}

// signatureBegins are the first lines of the OpenPGP, X.509 and SSH
// signature formats that git recognizes.
var signatureBegins = []string{
	"-----BEGIN PGP SIGNATURE-----",
	"-----BEGIN PGP MESSAGE-----",
	"-----BEGIN CERTIFICATE-----",
	"-----BEGIN SIGNED MESSAGE-----",
	"-----BEGIN SSH SIGNATURE-----",
}

// signatureStart returns the offset of the last line of msg that begins a
// signature, or -1. Like git's parse_signed_buffer, it treats everything
// from there on as the signature.
func signatureStart(msg string) int {
	match := -1
	for n := 0; n < len(msg); {
		for _, begin := range signatureBegins {
			if strings.HasPrefix(msg[n:], begin) {
				match = n
				break
			}
		}
		eol := strings.IndexByte(msg[n:], '\n')
		if eol < 0 {
			break
		}
		n += eol + 1
	}
	return match
}

func parseObjectType(s string) ObjectType {
	switch s {
	case "commit":
		return CommitObject
	case "tree":
		return TreeObject
	case "blob":
		return BlobObject
	case "tag":
		return TagObject
	}
	return InvalidObject
}

// ID returns the tag's hash.
func (t *Tag) ID() Hash { return t.Hash }

// Type returns TagObject.
func (t *Tag) Type() ObjectType { return TagObject }

// Commit returns the commit the tag points at, or ErrUnsupportedObject if
// it points at something else.
func (t *Tag) Commit() (*Commit, error) {
	if t.TargetType != CommitObject {
		return nil, ErrUnsupportedObject
	}
	return t.r.CommitObject(t.Target)
}

// Tree returns the tree the tag points at, or the commit's tree if it points
// at a commit.
func (t *Tag) Tree() (*Tree, error) {
	switch t.TargetType {
	case CommitObject:
		c, err := t.Commit()
		if err != nil {
			return nil, err
		}
		return c.Tree()
	case TreeObject:
		return t.r.TreeObject(t.Target)
	}
	return nil, ErrUnsupportedObject
}

// Blob returns the blob the tag points at.
func (t *Tag) Blob() (*Blob, error) {
	if t.TargetType != BlobObject {
		return nil, ErrUnsupportedObject
	}
	return t.r.BlobObject(t.Target)
}
