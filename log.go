package git2

import (
	"encoding/binary"
	"errors"
	"io"
	"time"
)

// LogOrder selects the order of commits returned by Log.
type LogOrder int8

const (
	// LogOrderDefault is depth-first pre-order, first parents first.
	LogOrderDefault LogOrder = iota
	LogOrderDFS
	LogOrderDFSPost
	LogOrderBSF
	// LogOrderCommitterTime is git log's default order, newest committer
	// time first.
	LogOrderCommitterTime
)

// Walk orders.
const (
	orderPreorder = 0
	orderTime     = 1
)

// ErrUnsupportedOrder is returned by Log for LogOrderDFSPost and
// LogOrderBSF.
var ErrUnsupportedOrder = errors.New("unsupported log order")

// LogOptions select the commits that Log returns and their order.
type LogOptions struct {
	// From is the commit to start at. If it is zero and All is false, the
	// walk starts at HEAD.
	From Hash
	// Order of the returned commits.
	Order LogOrder
	// FileName selects commits that change the file or directory at this
	// path. For a directory, that is any change under it. It is evaluated
	// without diffing trees and is much faster than PathFilter. It selects
	// commits by the same rule as PathFilter, and when both are set, a
	// commit must pass both.
	FileName *string
	// PathFilter selects commits that change at least one path for which
	// it returns true. Paths are full, slash-separated file paths.
	//
	// A commit is selected unless it is identical to one of its parents on
	// the filtered paths, which is git's TREESAME rule. A root commit is
	// selected if any filtered path exists. This selects what
	// git log -- <path> does, except that side branches are never pruned.
	// When a merge's side branch made the same change as the main line, the
	// side commit is selected too.
	//
	// Unlike go-git, which diffs each commit against the next one in
	// iteration order, PathFilter compares each commit with its parents. The
	// results differ at merges and, with All, across branches.
	//
	// PathFilter runs while the walk holds the Repository, so it must not
	// call the Repository's methods.
	PathFilter func(string) bool
	// All walks from every reference (and HEAD) instead of From. Unlike
	// go-git, which misses commits that only some references reach, the
	// walk returns every commit reachable from any of them.
	All bool
	// Since and Until bound the committer time, inclusively. In
	// LogOrderCommitterTime, the walk stops at the first commit older than
	// Since, as git log does.
	Since *time.Time
	Until *time.Time
}

// Log returns the history selected by o. Close the iterator when stopping
// early. ForEach closes it for you.
//
// Unlike go-git, Log walks a shallow clone up to its shallow boundary
// instead of failing on the missing parents.
func (r *Repository) Log(o *LogOptions) (*CommitIter, error) {
	if o == nil {
		o = &LogOptions{}
	}
	order := int32(orderPreorder)
	switch o.Order {
	case LogOrderDefault, LogOrderDFS:
	case LogOrderCommitterTime:
		order = orderTime
	default:
		return nil, ErrUnsupportedOrder
	}

	from := o.From
	if !o.All && from.IsZero() {
		head, err := r.Head()
		if err != nil {
			return nil, err
		}
		from = head.Hash()
	}

	var roots []Hash
	if o.All {
		var err error
		if roots, err = r.allRoots(); err != nil {
			return nil, err
		}
	}

	s := walkSpec{order: order, from: from, all: o.All, roots: roots, fileName: o.FileName, filter: o.PathFilter}
	if o.Since != nil {
		// Commit times are whole seconds, so t < since exactly when
		// t < ceil(since).
		s.hasSince, s.since = true, o.Since.Unix()
		if o.Since.Nanosecond() != 0 {
			s.since++
		}
	}
	if o.Until != nil {
		s.hasUntil, s.until = true, o.Until.Unix()
	}
	return r.startWalk(&s)
}

// walkSpec is a history walk's parameters, as Log resolved them.
type walkSpec struct {
	order    int32 // orderPreorder or orderTime
	from     Hash  // unless all
	all      bool
	roots    []Hash // with all
	fileName *string
	filter   func(string) bool

	// Committer-time bounds in seconds, inclusive.
	hasSince, hasUntil bool
	since, until       int64
}

func b2i(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// allRoots returns the commits that HEAD and every reference peel to.
func (r *Repository) allRoots() (_ []Hash, err error) {
	refs, err := r.References()
	if err != nil {
		return nil, err
	}
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	roots := make([]Hash, 0, len(refs.refs))
	for _, ref := range refs.refs {
		h := ref.Hash()
		if ref.Type() == SymbolicReference {
			res, err := r.reference(rt, ref.Name(), true)
			if err != nil {
				continue
			}
			h = res.Hash()
		}
		code := rt.mod.Xg2_peel(r.repo, rt.putOID(oidA, h), objCommit, rt.slot(0))
		if code < 0 {
			continue // not a commit (e.g. a tag of a tree)
		}
		roots = append(roots, rt.hashAt(uint32(rt.slot(0))))
	}
	return roots, nil
}

// logBatch is the number of commits a walk selects per fill.
const logBatch = 256

// CommitIter iterates over commits, either from a history walk or from a
// fixed list of ids.
type CommitIter struct {
	r     *Repository
	list  bool    // iterates ids rather than a walk
	walk  *walker // the history walk, while live
	ids   []Hash  // list-backed: remaining ids
	buf   []byte  // walk-backed: records from fill, reused per batch
	batch []byte  // unconsumed part of buf
	done  bool    // no more records will be fetched
	err   error
}

// record returns the next commit's id and raw object, or io.EOF.
func (it *CommitIter) record() (Hash, []byte, error) {
	for len(it.batch) == 0 {
		if it.err != nil {
			return ZeroHash, nil, it.err
		}
		if it.done {
			return ZeroHash, nil, io.EOF
		}
		it.fill()
	}
	var h Hash
	copy(h[:], it.batch)
	n := binary.LittleEndian.Uint32(it.batch[20:])
	raw := it.batch[24 : 24+n]
	it.batch = it.batch[24+n:]
	return h, raw, nil
}

// NextHash returns the next commit id without decoding the commit, or
// io.EOF.
func (it *CommitIter) NextHash() (Hash, error) {
	if it.list {
		return it.nextID()
	}
	h, _, err := it.record()
	return h, err
}

func (it *CommitIter) nextID() (Hash, error) {
	if len(it.ids) == 0 {
		return ZeroHash, io.EOF
	}
	h := it.ids[0]
	it.ids = it.ids[1:]
	return h, nil
}

// Next returns the next commit, or io.EOF.
func (it *CommitIter) Next() (*Commit, error) {
	if it.list {
		h, err := it.nextID()
		if err != nil {
			return nil, err
		}
		return it.r.CommitObject(h)
	}
	h, raw, err := it.record()
	if err != nil {
		return nil, err
	}
	return decodeCommit(it.r, h, string(raw))
}

// ForEach calls fn for each remaining commit, then closes the iterator.
// Returning ErrStop from fn ends the iteration without error.
func (it *CommitIter) ForEach(fn func(*Commit) error) error {
	defer it.Close()
	for {
		c, err := it.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(c); err != nil {
			if err == ErrStop {
				return nil
			}
			return err
		}
	}
}

// Close ends the walk and releases its memory.
func (it *CommitIter) Close() {
	it.closeWalk()
	it.done, it.ids, it.buf, it.batch = true, nil, nil, nil
}
