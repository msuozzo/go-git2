package git2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"time"
)

// The history walk reads commits from libgit2's object database and walks
// them in Go.

// libgit2 error classes (git2/errors.h) for errors raised in Go.
const (
	errClassInvalid = 3
	errClassTree    = 14
	errClassGrafts  = 36
)

type walker struct {
	r *Repository

	// orderTime: commits waiting to be visited, newest first.
	byTime bool
	queue  []queued
	pushed uint64 // commits pushed so far, which orders ties

	// orderPreorder: roots are visited in push order.
	roots    []Hash
	rootNext int
	stack    []frame
	parents  []parentRef // parents of the commits on stack, contiguous

	seen map[Hash]struct{}

	hasSince, hasUntil bool
	since, until       int64

	paths       []string          // exact paths (FileName)
	filter      func(string) bool // PathFilter
	filterPanic *filterPanic      // a panic in filter, re-raised by fill
	path        []byte            // path being compared
	depth       int               // of the trees being compared

	grafts, shallow   map[Hash][]Hash
	scratch, pscratch []parentRef          // parents of the commit and of a parent being read
	praw              []byte               // parent commits read for the filter
	trees, oldTrees   map[Hash][]TreeEntry // trees of this and the previous comparison
}

type frame struct {
	parents int // index of the first parent in walker.parents
	n       int
	next    int
}

// parentRef is a parent id. bad marks a parent line whose id does not
// parse, which fails the walk only if the parent is visited.
type parentRef struct {
	h   Hash
	bad bool
}

// queued is a commit waiting in the committer-time queue. seq is its push
// order.
type queued struct {
	id   Hash
	time int64
	seq  uint64
}

// rawCommit is the part of a commit header the walk needs.
type rawCommit struct {
	tree    Hash
	parents []parentRef
	time    int64
}

func (r *Repository) startWalk(s *walkSpec) (_ *CommitIter, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)

	w := &walker{
		r: r, seen: map[Hash]struct{}{},
		hasSince: s.hasSince, since: s.since, hasUntil: s.hasUntil, until: s.until,
	}
	if w.grafts, w.shallow, err = r.loadGrafts(rt); err != nil {
		return nil, err
	}
	// From must be a commit. go-git reports an error for an annotated tag
	// instead of peeling it.
	if !s.all {
		if t, _, err := r.header(rt, s.from); err != nil || t != CommitObject {
			return nil, ErrObjectNotFound
		}
		w.roots = append(w.roots, s.from)
	}
	w.roots = append(w.roots, s.roots...)
	if s.order == orderTime {
		w.byTime = true
		for _, h := range w.roots {
			if err := w.enqueue(rt, h); err != nil {
				return nil, err
			}
		}
	}
	if s.fileName != nil {
		w.paths = []string{*s.fileName}
	}
	w.filter = s.filter
	return &CommitIter{r: r, walk: w}, nil
}

// fill reads the next batch of selected commits into it.buf. Each commit is
// stored as its id, the u32 length of its raw contents, and the contents.
func (it *CommitIter) fill() {
	it.fillLocked()
	if w := it.walk; w != nil && w.filterPanic != nil {
		p := w.filterPanic
		w.filterPanic = nil
		panic(p.v)
	}
}

func (it *CommitIter) fillLocked() {
	rt, err := it.r.lock()
	if err != nil {
		it.err = err
		return
	}
	defer it.r.unlock(&it.err)
	w := it.walk
	buf := it.buf[:0]
	n := 0
	for n < logBatch {
		start := len(buf)
		var id Hash
		var c rawCommit
		var ok bool
		buf = append(buf, make([]byte, 24)...)
		if w.byTime {
			id, buf, c, ok, err = w.timeNext(rt, buf)
		} else {
			id, buf, c, ok, err = w.preorderNext(rt, buf)
		}
		if err != nil || !ok {
			buf = buf[:start]
			break
		}
		if (w.hasSince && c.time < w.since) || (w.hasUntil && c.time > w.until) {
			buf = buf[:start]
			continue
		}
		var pass bool
		if pass, err = w.passes(rt, &c); err != nil {
			buf = buf[:start]
			break
		}
		if !pass {
			buf = buf[:start]
			continue
		}
		copy(buf[start:], id[:])
		binary.LittleEndian.PutUint32(buf[start+20:], uint32(len(buf)-start-24))
		n++
	}
	it.buf = buf
	if err != nil {
		// The commits selected before the error come first.
		it.err = err
		if n > 0 {
			it.batch = it.buf
		}
		return
	}
	if n == 0 {
		it.done = true
		it.walk = nil
		return
	}
	it.batch = it.buf
}

// closeWalk releases the walk.
func (it *CommitIter) closeWalk() { it.walk = nil }

// timeNext appends the next commit in committer-time order to dst and
// queues its parents, as git's walk does. With Since, the walk ends at the
// first commit older than Since. ok is false at the end of history.
func (w *walker) timeNext(rt *instance, dst []byte) (id Hash, out []byte, c rawCommit, ok bool, err error) {
	if len(w.queue) == 0 {
		return id, dst, c, false, nil
	}
	q := w.pop()
	if w.hasSince && q.time < w.since {
		// Every queued commit is older still.
		w.queue = w.queue[:0]
		return id, dst, c, false, nil
	}
	start := len(dst)
	if dst, c, err = w.readCommit(rt, q.id, dst, &w.scratch); err != nil {
		return q.id, dst, c, false, err
	}
	for _, p := range c.parents {
		if p.bad {
			return q.id, dst[:start], c, false, errBadOID
		}
		if err = w.enqueue(rt, p.h); err != nil {
			return q.id, dst[:start], c, false, err
		}
	}
	return q.id, dst, c, true, nil
}

// enqueue reads commit h for its time and queues it, unless it was queued
// before.
func (w *walker) enqueue(rt *instance, h Hash) error {
	if _, dup := w.seen[h]; dup {
		return nil
	}
	var c rawCommit
	var err error
	if w.praw, c, err = w.readCommit(rt, h, w.praw[:0], &w.pscratch); err != nil {
		return err
	}
	w.seen[h] = struct{}{}
	w.push(queued{id: h, time: c.time, seq: w.pushed})
	w.pushed++
	return nil
}

// before reports whether a leaves the queue before b: newer first, and
// then in push order.
func (a *queued) before(b *queued) bool {
	return a.time > b.time || a.time == b.time && a.seq < b.seq
}

func (w *walker) push(q queued) {
	w.queue = append(w.queue, q)
	for i := len(w.queue) - 1; i > 0; {
		parent := (i - 1) / 2
		if !w.queue[i].before(&w.queue[parent]) {
			break
		}
		w.queue[i], w.queue[parent] = w.queue[parent], w.queue[i]
		i = parent
	}
}

func (w *walker) pop() queued {
	q := w.queue[0]
	last := len(w.queue) - 1
	w.queue[0] = w.queue[last]
	w.queue = w.queue[:last]
	for i := 0; ; {
		least, l, r := i, 2*i+1, 2*i+2
		if l < last && w.queue[l].before(&w.queue[least]) {
			least = l
		}
		if r < last && w.queue[r].before(&w.queue[least]) {
			least = r
		}
		if least == i {
			break
		}
		w.queue[i], w.queue[least] = w.queue[least], w.queue[i]
		i = least
	}
	return q
}

// preorderNext appends the next commit in pre-order to dst. ok is false at
// the end of history.
func (w *walker) preorderNext(rt *instance, dst []byte) (id Hash, out []byte, c rawCommit, ok bool, err error) {
	for {
		if len(w.stack) == 0 {
			if w.rootNext >= len(w.roots) {
				return id, dst, c, false, nil
			}
			id = w.roots[w.rootNext]
			w.rootNext++
		} else {
			top := &w.stack[len(w.stack)-1]
			if top.next >= top.n {
				w.parents = w.parents[:top.parents]
				w.stack = w.stack[:len(w.stack)-1]
				continue
			}
			p := w.parents[top.parents+top.next]
			top.next++
			if p.bad {
				return id, dst, c, false, errBadOID
			}
			id = p.h
		}
		if _, dup := w.seen[id]; dup {
			continue
		}
		if dst, c, err = w.readCommit(rt, id, dst, &w.scratch); err != nil {
			return id, dst, c, false, err
		}
		w.seen[id] = struct{}{}
		if len(c.parents) > 0 {
			w.stack = append(w.stack, frame{parents: len(w.parents), n: len(c.parents)})
			w.parents = append(w.parents, c.parents...)
		}
		return id, dst, c, true, nil
	}
}

var errBadOID = &Error{Code: -1, Class: errClassInvalid, Message: "unable to parse OID - contains invalid characters"}

// readCommit appends commit id's raw object to dst and parses its header,
// applying grafts. c.parents aliases *scratch.
func (w *walker) readCommit(rt *instance, id Hash, dst []byte, scratch *[]parentRef) ([]byte, rawCommit, error) {
	start := len(dst)
	dst, t, err := w.r.readRaw(rt, id, AnyObject, dst)
	if err != nil {
		if err == ErrObjectNotFound {
			err = rt.error(gitENotFound)
		}
		return dst, rawCommit{}, err
	}
	if t != CommitObject {
		return dst[:start], rawCommit{}, &Error{Code: gitENotFound, Class: errClassInvalid, Message: "object is not a commit"}
	}
	c, ok := parseCommitHeader(dst[start:], (*scratch)[:0])
	*scratch = c.parents
	if !ok {
		return dst[:start], rawCommit{}, &Error{Code: -1, Class: errClassInvalid, Message: "failed to parse commit: no tree"}
	}
	if g, ok := w.grafts[id]; ok {
		c.parents = graftParents(g, scratch)
	} else if g, ok := w.shallow[id]; ok {
		c.parents = graftParents(g, scratch)
	}
	return dst, c, nil
}

func graftParents(g []Hash, scratch *[]parentRef) []parentRef {
	ps := (*scratch)[:0]
	for _, h := range g {
		ps = append(ps, parentRef{h: h})
	}
	*scratch = ps
	return ps
}

// parseCommitHeader reads the tree, parents and committer time the way git
// does. The tree comes from the first line only, and the parents from the
// lines right after it. Later tree and parent lines are ignored. The
// committer time is read as Commit.Committer.When is: from the committer
// line in its place after the author, and zero without one. ok is false
// unless the first line holds a tree id.
func parseCommitHeader(raw []byte, parents []parentRef) (c rawCommit, ok bool) {
	c = rawCommit{parents: parents, time: zeroTime}
	pos := atTree
	for line := raw; len(line) > 0 && line[0] != '\n'; {
		eol := bytes.IndexByte(line, '\n')
		if eol < 0 {
			eol = len(line)
		}
		l := line[:eol]
		key, val, _ := bytes.Cut(l, []byte{' '})
		switch pos {
		case atTree:
			if len(l) <= 5 || string(l[:5]) != "tree " {
				return c, false
			}
			if c.tree, ok = parseHex(l[5:]); !ok {
				return c, false
			}
			pos = inParents
		case inParents:
			if len(l) > 7 && string(l[:7]) == "parent " {
				// Ids may run past the line, as libgit2 reads them.
				h, ok := parseHex(line[7:])
				c.parents = append(c.parents, parentRef{h: h, bad: !ok})
				break
			}
			pos = atAuthor
			fallthrough
		case atAuthor:
			pos = atCommitter
			if string(key) == "author" {
				break
			}
			fallthrough
		case atCommitter:
			pos = inHeaders
			if string(key) == "committer" {
				c.time = signatureTime(val)
			}
		}
		if pos == inHeaders || eol == len(line) {
			break
		}
		line = line[eol+1:]
	}
	return c, true
}

// zeroTime is the Unix time of the zero time.Time, which a commit without a
// committer time has.
var zeroTime = time.Time{}.Unix()

// passes reports whether the path filters select c. A commit must pass
// both FileName and PathFilter when both are set.
func (w *walker) passes(rt *instance, c *rawCommit) (bool, error) {
	if w.paths == nil && w.filter == nil {
		return true, nil
	}
	w.oldTrees, w.trees = w.trees, w.oldTrees
	clear(w.trees)
	if w.paths != nil {
		if ok, err := w.selects(rt, c, true); !ok || err != nil {
			return false, err
		}
	}
	if w.filter != nil {
		return w.selects(rt, c, false)
	}
	return true, nil
}

// selects reports whether one filter, the paths if byPath and otherwise
// the PathFilter function, selects c. Following git's TREESAME rule, c is
// selected unless it is identical to one of its parents on the filtered
// paths. A root commit is selected if any filtered path exists.
func (w *walker) selects(rt *instance, c *rawCommit, byPath bool) (bool, error) {
	if len(c.parents) == 0 {
		return w.differs(rt, c.tree, nil, byPath)
	}
	for _, p := range c.parents {
		if p.bad {
			return false, errBadOID
		}
		var pc rawCommit
		var err error
		w.praw, pc, err = w.readCommit(rt, p.h, w.praw[:0], &w.pscratch)
		if err != nil {
			return false, err
		}
		pt := pc.tree
		d, err := w.differs(rt, c.tree, &pt, byPath)
		if err != nil || !d {
			return false, err
		}
	}
	return true, nil
}

// differs reports whether the filtered paths differ between tree ct and tree
// pt (nil meaning the empty tree), by the paths if byPath and otherwise by
// the PathFilter function.
func (w *walker) differs(rt *instance, ct Hash, pt *Hash, byPath bool) (bool, error) {
	if pt != nil && ct == *pt {
		return false, nil
	}
	if byPath {
		for _, p := range w.paths {
			ma, ia, ea := w.r.entryByPath(rt, ct, p)
			mb, ib, eb := FileMode(0), ZeroHash, error(ErrObjectNotFound)
			if pt != nil {
				mb, ib, eb = w.r.entryByPath(rt, *pt, p)
			}
			if ea != nil && ea != ErrObjectNotFound {
				return false, ea
			}
			if eb != nil && eb != ErrObjectNotFound {
				return false, eb
			}
			if (ea == nil) != (eb == nil) || (ea == nil && (ma != mb || ia != ib)) {
				return true, nil
			}
		}
		return false, nil
	}
	ta, err := w.tree(rt, ct)
	if err != nil {
		return false, err
	}
	var tb []TreeEntry
	if pt != nil {
		if tb, err = w.tree(rt, *pt); err != nil {
			return false, err
		}
	}
	w.path = w.path[:0]
	return w.treeChanged(rt, ta, tb)
}

// tree returns the entries of tree h, read through the module.
func (w *walker) tree(rt *instance, h Hash) ([]TreeEntry, error) {
	if e, ok := w.trees[h]; ok {
		return e, nil
	}
	if e, ok := w.oldTrees[h]; ok {
		w.trees[h] = e
		return e, nil
	}
	raw, _, err := w.r.readString(rt, h, TreeObject)
	if err != nil {
		return nil, err
	}
	e, err := parseEntries(raw, nil)
	if err != nil {
		return nil, err
	}
	if w.trees == nil {
		w.trees, w.oldTrees = map[Hash][]TreeEntry{}, map[Hash][]TreeEntry{}
	}
	w.trees[h] = e
	return e, nil
}

// treeChanged calls the filter on every path that differs between trees a
// and b (nil meaning empty), stopping at the first match.
func (w *walker) treeChanged(rt *instance, a, b []TreeEntry) (bool, error) {
	for i := range a {
		ea := &a[i]
		eb := entryByName(b, ea.Name)
		if eb != nil && ea.Mode == eb.Mode && ea.Hash == eb.Hash {
			continue
		}
		if eb != nil && ea.Mode.isDir() != eb.Mode.isDir() {
			// Type change: report each side separately.
			if r, err := w.entryChanged(rt, ea, nil); r || err != nil {
				return r, err
			}
			if r, err := w.entryChanged(rt, nil, eb); r || err != nil {
				return r, err
			}
			continue
		}
		if r, err := w.entryChanged(rt, ea, eb); r || err != nil {
			return r, err
		}
	}
	for i := range b {
		eb := &b[i]
		if entryByName(a, eb.Name) != nil {
			continue
		}
		if r, err := w.entryChanged(rt, nil, eb); r || err != nil {
			return r, err
		}
	}
	return false, nil
}

func (w *walker) entryChanged(rt *instance, ea, eb *TreeEntry) (r bool, err error) {
	base := len(w.path)
	e := ea
	if e == nil {
		e = eb
	}
	if base > 0 {
		w.path = append(w.path, '/')
	}
	w.path = append(w.path, e.Name...)

	ta, tb := ea != nil && ea.Mode.isDir(), eb != nil && eb.Mode.isDir()
	if ta || tb {
		r, err = w.subtreeChanged(rt, ta, ea, tb, eb)
	}
	// A blob replaced by a tree (or vice versa) changes both.
	if !r && err == nil && ((ea != nil && !ta) || (eb != nil && !tb)) {
		r, err = w.match(string(w.path))
	}
	w.path = w.path[:base]
	return r, err
}

type filterPanic struct{ v any }

var errFilterPanic = errors.New("git2: PathFilter panicked")

// match calls the caller's filter. A panic there belongs to the caller and
// is not a fault in the module. It ends the walk, and fill re-raises it
// once the instance is released.
func (w *walker) match(p string) (ok bool, err error) {
	defer func() {
		if v := recover(); v != nil {
			w.filterPanic = &filterPanic{v}
			err = errFilterPanic
		}
	}()
	return w.filter(p), nil
}

func (w *walker) subtreeChanged(rt *instance, ta bool, ea *TreeEntry, tb bool, eb *TreeEntry) (bool, error) {
	var a, b []TreeEntry
	var err error
	if ta {
		if a, err = w.tree(rt, ea.Hash); err != nil {
			return false, err
		}
	}
	if tb {
		if b, err = w.tree(rt, eb.Hash); err != nil {
			return false, err
		}
	}
	if w.depth >= maxTreeDepth {
		return false, errTreeDepth
	}
	w.depth++
	r, err := w.treeChanged(rt, a, b)
	w.depth--
	return r, err
}

// maxTreeDepth bounds tree nesting, here and in treeWalk, as git's
// core.maxTreeDepth does. A deeper or self-containing tree is an error
// instead of a stack overflow.
const maxTreeDepth = 2048

var errTreeDepth = &Error{Code: -1, Class: errClassTree, Message: "tree is nested too deeply"}

// entryByName finds the entry named name in entries, which are in git's
// tree order, where directories sort as if their names ended in '/'.
func entryByName(entries []TreeEntry, name string) *TreeEntry {
	for _, dir := range [2]bool{false, true} {
		lo, hi := 0, len(entries)
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			if treeLess(&entries[mid], name, dir) {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo < len(entries) && entries[lo].Name == name {
			return &entries[lo]
		}
	}
	return nil
}

// treeLess reports whether e sorts before an entry named name (a directory
// if dir) in git's tree order.
func treeLess(e *TreeEntry, name string, dir bool) bool {
	a, b := e.Name, name
	n := min(len(a), len(b))
	if c := strings.Compare(a[:n], b[:n]); c != 0 {
		return c < 0
	}
	ca, cb := byte(0), byte(0)
	if len(a) > n {
		ca = a[n]
	} else if e.Mode.isDir() {
		ca = '/'
	}
	if len(b) > n {
		cb = b[n]
	} else if dir {
		cb = '/'
	}
	return ca < cb
}

// gitDir returns the repository's directory, or its common directory if
// common is set, as a path in r.fsys. The path is "" for the root of r.fsys
// and otherwise ends in a slash.
func (r *Repository) gitDir(rt *instance, common bool) (string, error) {
	p := rt.cstring(uint32(rt.mod.Xg2_repository_path(r.repo, b2i(common))))
	rel, ok := strings.CutPrefix(p, mount+"/")
	if !ok {
		return "", fmt.Errorf("git2: repository path %q is outside the repository", p)
	}
	return rel, nil
}

// loadGrafts reads info/grafts and shallow as libgit2 does when it opens
// the repository.
func (r *Repository) loadGrafts(rt *instance) (grafts, shallow map[Hash][]Hash, err error) {
	common, err := r.gitDir(rt, true)
	if err != nil {
		return nil, nil, err
	}
	dir, err := r.gitDir(rt, false)
	if err != nil {
		return nil, nil, err
	}
	if grafts, err = r.readGrafts(common + "info/grafts"); err != nil {
		return nil, nil, err
	}
	shallow, err = r.readGrafts(dir + "shallow")
	return grafts, shallow, err
}

// readGrafts parses a grafts file, whose format the shallow file shares.
// Each line is a commit id followed by the ids of its replacement parents.
// A missing file has no grafts.
func (r *Repository) readGrafts(name string) (map[Hash][]Hash, error) {
	f, err := r.fsys.Open(name)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(f); err != nil {
		return nil, err
	}
	g := map[Hash][]Hash{}
	for n, rest := 1, b.String(); len(rest) > 0; n++ {
		var line string
		line, rest = cutLine(rest)
		id, ok := parseHex(line)
		if !ok {
			return nil, &Error{Code: -1, Class: errClassGrafts, Message: fmt.Sprintf("invalid graft OID at line %d", n)}
		}
		var parents []Hash
		for line = line[40:]; len(line) > 0; line = line[41:] {
			p, ok := parseHex(line[min(1, len(line)):])
			if line[0] != ' ' || !ok {
				return nil, &Error{Code: -1, Class: errClassGrafts, Message: fmt.Sprintf("invalid parent OID at line %d", n)}
			}
			parents = append(parents, p)
		}
		g[id] = parents
	}
	return g, nil
}

// isNotExist reports whether err means a missing file. Like libgit2, it
// treats both ENOENT and ENOTDIR as missing.
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}
