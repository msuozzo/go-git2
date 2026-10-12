package git2

import (
	"io"
	"strings"
)

// ReferenceType is the kind of a reference.
type ReferenceType int8

const (
	InvalidReference  ReferenceType = 0
	HashReference     ReferenceType = 1
	SymbolicReference ReferenceType = 2
)

func (t ReferenceType) String() string {
	switch t {
	case HashReference:
		return "hash-reference"
	case SymbolicReference:
		return "symbolic-reference"
	}
	return "invalid-reference"
}

// ReferenceName is a full reference name such as refs/heads/main.
type ReferenceName string

const (
	HEAD   ReferenceName = "HEAD"
	Master ReferenceName = "refs/heads/master"
	Main   ReferenceName = "refs/heads/main"
)

const (
	refPrefix       = "refs/"
	refHeadPrefix   = refPrefix + "heads/"
	refTagPrefix    = refPrefix + "tags/"
	refRemotePrefix = refPrefix + "remotes/"
	refNotePrefix   = refPrefix + "notes/"
)

// NewBranchReferenceName returns refs/heads/<name>.
func NewBranchReferenceName(name string) ReferenceName {
	return ReferenceName(refHeadPrefix + name)
}

// NewTagReferenceName returns refs/tags/<name>.
func NewTagReferenceName(name string) ReferenceName { return ReferenceName(refTagPrefix + name) }

// NewRemoteReferenceName returns refs/remotes/<remote>/<name>.
func NewRemoteReferenceName(remote, name string) ReferenceName {
	return ReferenceName(refRemotePrefix + remote + "/" + name)
}

func (r ReferenceName) String() string { return string(r) }
func (r ReferenceName) IsBranch() bool { return strings.HasPrefix(string(r), refHeadPrefix) }
func (r ReferenceName) IsTag() bool    { return strings.HasPrefix(string(r), refTagPrefix) }
func (r ReferenceName) IsRemote() bool { return strings.HasPrefix(string(r), refRemotePrefix) }
func (r ReferenceName) IsNote() bool   { return strings.HasPrefix(string(r), refNotePrefix) }

// Short returns the shortest unambiguous form of the name.
// For example, refs/heads/main becomes main, and refs/remotes/origin/main
// becomes origin/main.
func (r ReferenceName) Short() string {
	s := string(r)
	for _, p := range [...]string{refHeadPrefix, refTagPrefix, refRemotePrefix, refPrefix} {
		if strings.HasPrefix(s, p) {
			return s[len(p):]
		}
	}
	return s
}

// Reference is a named pointer to an object or to another reference.
type Reference struct {
	t      ReferenceType
	n      ReferenceName
	h      Hash
	target ReferenceName
}

// NewHashReference returns a direct reference.
func NewHashReference(n ReferenceName, h Hash) *Reference {
	return &Reference{t: HashReference, n: n, h: h}
}

// NewSymbolicReference returns a symbolic reference.
func NewSymbolicReference(n, target ReferenceName) *Reference {
	return &Reference{t: SymbolicReference, n: n, target: target}
}

func (r *Reference) Type() ReferenceType   { return r.t }
func (r *Reference) Name() ReferenceName   { return r.n }
func (r *Reference) Hash() Hash            { return r.h }
func (r *Reference) Target() ReferenceName { return r.target }

func (r *Reference) String() string {
	if r.t == SymbolicReference {
		return "ref: " + string(r.target) + " " + string(r.n)
	}
	return r.h.String() + " " + string(r.n)
}

// refBuilder makes References from libgit2 references. It allocates them
// from slabs of References and blocks of names.
type refBuilder struct {
	slab  []Reference
	names strBlocks
}

func (b *refBuilder) add(rt *instance, ref int32) *Reference {
	id := rt.buf + oidB
	rt.mod.Xg2_ref_info(ref, rt.slot(1), rt.slot(2), rt.slot(3), int32(id), rt.slot(4), rt.slot(5))
	if len(b.slab) == cap(b.slab) {
		b.slab = make([]Reference, 0, min(max(2*cap(b.slab), 1), 256))
	}
	b.slab = append(b.slab, Reference{n: ReferenceName(b.names.carve(rt.bytes(rt.slotU32(2), rt.slotU32(3))))})
	r := &b.slab[len(b.slab)-1]
	if rt.slotU32(1) != 0 {
		r.t = SymbolicReference
		r.target = ReferenceName(b.names.carve(rt.bytes(rt.slotU32(4), rt.slotU32(5))))
	} else {
		r.t = HashReference
		r.h = rt.hashAt(id)
	}
	return r
}

// ReferenceIter iterates over references.
type ReferenceIter struct {
	refs []*Reference
	pos  int
}

// Next returns the next reference, or io.EOF when exhausted.
func (it *ReferenceIter) Next() (*Reference, error) {
	if it.pos >= len(it.refs) {
		return nil, io.EOF
	}
	r := it.refs[it.pos]
	it.pos++
	return r, nil
}

// ForEach calls fn for each remaining reference. Returning ErrStop from fn
// ends the iteration without error.
func (it *ReferenceIter) ForEach(fn func(*Reference) error) error {
	for ; it.pos < len(it.refs); it.pos++ {
		if err := fn(it.refs[it.pos]); err != nil {
			if err == ErrStop {
				return nil
			}
			return err
		}
	}
	return nil
}

// Close releases the iterator.
func (it *ReferenceIter) Close() { it.refs, it.pos = nil, 0 }

// Len returns the total number of references in the iterator.
func (it *ReferenceIter) Len() int { return len(it.refs) }
