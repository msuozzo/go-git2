package git2

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
)

// TreeEntry is one entry of a tree.
//
// Unlike go-git, which rejects entries that git refuses to check out, such
// as .git, .. or names with control characters, Name holds the name as
// stored. Check it before using it as a filesystem path.
type TreeEntry struct {
	Name string
	Mode FileMode
	Hash Hash
}

// Tree is a parsed tree object.
type Tree struct {
	Hash    Hash
	Entries []TreeEntry

	r *Repository
}

// decode parses a raw tree. Entry names alias it.
func (t *Tree) decode(raw string) error {
	n := 0
	for b := raw; len(b) > 0; n++ {
		z := strings.IndexByte(b, 0)
		if z < 0 || z+21 > len(b) {
			break
		}
		b = b[z+21:]
	}
	var err error
	t.Entries, err = parseEntries(raw, make([]TreeEntry, 0, n))
	return err
}

// ID returns the tree's hash.
func (t *Tree) ID() Hash { return t.Hash }

// Type returns TreeObject.
func (t *Tree) Type() ObjectType { return TreeObject }

// FindEntry returns the entry at the slash-separated path p, or
// ErrEntryNotFound. Unlike go-git, it returns ErrEntryNotFound for a missing
// intermediate directory too, not ErrDirectoryNotFound.
func (t *Tree) FindEntry(p string) (_ *TreeEntry, err error) {
	if !strings.Contains(p, "/") {
		for i := range t.Entries {
			if t.Entries[i].Name == p {
				e := t.Entries[i]
				return &e, nil
			}
		}
		return nil, ErrEntryNotFound
	}
	rt, err := t.r.lock()
	if err != nil {
		return nil, err
	}
	defer t.r.unlock(&err)
	mode, h, err := t.r.entryByPath(rt, t.Hash, p)
	if err != nil {
		if err == ErrObjectNotFound {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &TreeEntry{Name: path.Base(p), Mode: mode, Hash: h}, nil
}

// File returns the file at path p, or ErrFileNotFound if p is missing or
// is not a file.
func (t *Tree) File(p string) (*File, error) { return t.r.file(t.Hash, p) }

func (r *Repository) file(tree Hash, p string) (_ *File, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	mode, h, err := r.entryByPath(rt, tree, p)
	if err != nil {
		if err == ErrObjectNotFound {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	if mode == Dir || mode == Submodule {
		return nil, ErrFileNotFound
	}
	return &File{Name: p, Mode: mode, Hash: h, r: r}, nil
}

// Tree returns the subtree at path p, or ErrDirectoryNotFound.
func (t *Tree) Tree(p string) (*Tree, error) {
	e, err := t.FindEntry(p)
	if err == ErrEntryNotFound {
		return nil, ErrDirectoryNotFound
	}
	if err != nil {
		return nil, err
	}
	if e.Mode != Dir {
		return nil, ErrDirectoryNotFound
	}
	return t.r.TreeObject(e.Hash)
}

// TreeEntryFile returns the file for entry e of this tree.
func (t *Tree) TreeEntryFile(e *TreeEntry) (*File, error) {
	return &File{Name: e.Name, Mode: e.Mode, Hash: e.Hash, r: t.r}, nil
}

// Files iterates over every file in the tree, recursively. Directories and
// submodules are skipped.
func (t *Tree) Files() *FileIter { return &FileIter{r: t.r, tree: t.Hash} }

// File is a blob reached through a tree, named by its full path.
//
// Unlike go-git, File does not embed a Blob with a size computed up
// front. Walks read only tree objects, and a file's contents and size are
// loaded when requested.
type File struct {
	Name string
	Mode FileMode
	Hash Hash

	r *Repository
}

// Contents returns the file's contents.
func (f *File) Contents() (string, error) { return f.r.blobString(f.Hash) }

// Bytes appends the file's contents to dst.
func (f *File) Bytes(dst []byte) ([]byte, error) { return f.r.ReadBlob(f.Hash, dst) }

// Reader returns a reader over the file's contents.
func (f *File) Reader() (io.ReadCloser, error) { return blobReader(f.r, f.Hash) }

// Size returns the size of the file's contents.
func (f *File) Size() (int64, error) {
	_, n, err := f.r.ObjectHeader(f.Hash)
	return n, err
}

// Blob returns the file's blob.
func (f *File) Blob() (*Blob, error) { return f.r.BlobObject(f.Hash) }

// Lines returns the file's contents split into lines, without the final
// empty line when the contents end in a newline.
func (f *File) Lines() ([]string, error) {
	s, err := f.Contents()
	if err != nil {
		return nil, err
	}
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// Blob is a blob object. Its contents are read on demand.
type Blob struct {
	Hash Hash
	Size int64

	r *Repository
}

// ID returns the blob's hash.
func (b *Blob) ID() Hash { return b.Hash }

// Type returns BlobObject.
func (b *Blob) Type() ObjectType { return BlobObject }

// Reader returns a reader over the blob's contents.
func (b *Blob) Reader() (io.ReadCloser, error) { return blobReader(b.r, b.Hash) }

// Bytes appends the blob's contents to dst.
func (b *Blob) Bytes(dst []byte) ([]byte, error) { return b.r.ReadBlob(b.Hash, dst) }

func blobReader(r *Repository, h Hash) (io.ReadCloser, error) {
	b, err := r.ReadBlob(h, nil)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// walkEntry is an entry of a recursive tree walk, named by its full path.
type walkEntry struct {
	path string
	mode FileMode
	hash Hash
}

// treeWalk lists tree treeID recursively in pre-order, the order of git
// ls-tree -r -t, with one g2_tree_entry call per entry. It returns 0 or a
// libgit2 error code. A tree nested too deeply is reported as errTreeDepth.
func (rt *instance) treeWalk(repo, treeID, flags int32) ([]walkEntry, int32, error) {
	w := treeWalker{rt: rt, repo: repo, flags: flags}
	code, err := w.walk(treeID, 1)
	return w.entries, code, err
}

type treeWalker struct {
	rt          *instance
	repo, flags int32
	path        []byte    // the entry being visited
	paths       strBlocks // the entries' paths
	entries     []walkEntry
}

// walk lists the tree whose id is at address id.
func (w *treeWalker) walk(id int32, depth int) (int32, error) {
	if depth > maxTreeDepth {
		return -1, errTreeDepth
	}
	rt := w.rt
	if code := rt.mod.Xg2_tree_lookup(w.repo, id, rt.slot(4), rt.slot(5)); code < 0 {
		return code, nil
	}
	tree, n := int32(rt.slotU32(4)), int32(rt.slotU32(5))
	defer rt.mod.Xg2_tree_free(tree)
	entryID := int32(rt.buf + oidB)
	base := len(w.path)
	for i := range n {
		if code := rt.mod.Xg2_tree_entry(tree, i, rt.slot(4), entryID, rt.slot(5), rt.slot(6)); code < 0 {
			return code, nil
		}
		mode := canonicalMode(FileMode(rt.slotU32(4)))
		isTree := mode.isDir()
		w.path = w.path[:base]
		if base > 0 {
			w.path = append(w.path, '/')
		}
		w.path = append(w.path, rt.bytes(rt.slotU32(5), rt.slotU32(6))...)
		if (!isTree || w.flags&walkTrees != 0) && (mode != Submodule || w.flags&walkSubmodules != 0) {
			w.entries = append(w.entries, walkEntry{path: w.paths.carve(w.path), mode: mode, hash: rt.hashAt(uint32(entryID))})
		}
		if isTree {
			// The subtree's lookup reads entryID before its own entries
			// overwrite it.
			if code, err := w.walk(entryID, depth+1); code < 0 || err != nil {
				return code, err
			}
		}
	}
	w.path = w.path[:base]
	return 0, nil
}

// strBlocks carves strings from shared blocks, so building many small
// strings allocates a few blocks instead of one string each. Blocks start
// small and double, up to 64 KiB. A block is a strings.Builder that is
// never grown past its capacity, so earlier strings stay valid. It must
// not be copied after first use.
type strBlocks struct{ b strings.Builder }

func (s *strBlocks) carve(p []byte) string {
	if c := s.b.Cap(); len(p) > c-s.b.Len() {
		s.b = strings.Builder{}
		s.b.Grow(max(min(2*c, 64<<10), 256, len(p)))
	}
	n := s.b.Len()
	s.b.Write(p)
	return s.b.String()[n:]
}

// treeWalk flags.
const (
	walkTrees      = 1
	walkSubmodules = 2
)

// walk lists tree h recursively. See treeWalk.
func (r *Repository) walk(h Hash, flags int32) (_ []walkEntry, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	ents, code, err := rt.treeWalk(r.repo, rt.putOID(oidA, h), flags)
	if err != nil {
		return nil, err
	}
	if code < 0 {
		return nil, objectNotFound(rt, code)
	}
	return ents, nil
}

// FileIter iterates over the files of a tree, recursively, in tree order.
type FileIter struct {
	r    *Repository
	tree Hash
	ents []walkEntry
	err  error
	init bool
	slab []File
}

func (it *FileIter) load() {
	if !it.init {
		it.init = true
		it.ents, it.err = it.r.walk(it.tree, 0)
	}
}

// Next returns the next file, or io.EOF.
func (it *FileIter) Next() (*File, error) {
	it.load()
	if it.err != nil {
		return nil, it.err
	}
	if len(it.ents) == 0 {
		return nil, io.EOF
	}
	if len(it.slab) == 0 {
		it.slab = make([]File, 128)
	}
	f := &it.slab[0]
	it.slab = it.slab[1:]
	e := &it.ents[0]
	it.ents = it.ents[1:]
	*f = File{Name: e.path, Mode: e.mode, Hash: e.hash, r: it.r}
	return f, nil
}

// ForEach calls fn for each remaining file. Returning ErrStop ends the
// iteration without error.
func (it *FileIter) ForEach(fn func(*File) error) error {
	for {
		f, err := it.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(f); err != nil {
			if err == ErrStop {
				return nil
			}
			return err
		}
	}
}

// Close releases the iterator.
func (it *FileIter) Close() { it.ents, it.init, it.slab = nil, true, nil }

// Paths calls fn with the path, mode and hash of every file in the tree,
// recursively, without allocating per file.
func (t *Tree) Paths(fn func(path string, mode FileMode, h Hash) error) error {
	ents, err := t.r.walk(t.Hash, 0)
	if err != nil {
		return err
	}
	for i := range ents {
		if err := fn(ents[i].path, ents[i].mode, ents[i].hash); err != nil {
			if err == ErrStop {
				return nil
			}
			return err
		}
	}
	return nil
}

// TreeWalker walks a tree in pre-order, yielding directories as well as
// files.
type TreeWalker struct {
	r         *Repository
	tree      Hash
	recursive bool
	ents      []walkEntry
	entries   []TreeEntry
	err       error
	init      bool
}

// NewTreeWalker returns a walker over t. Non-recursive walkers yield only
// t's direct entries. Unlike go-git, it takes no seen map.
func NewTreeWalker(t *Tree, recursive bool) *TreeWalker {
	return &TreeWalker{r: t.r, tree: t.Hash, recursive: recursive, entries: t.Entries}
}

// Next returns the next entry's full path and entry, or io.EOF.
func (w *TreeWalker) Next() (string, TreeEntry, error) {
	if !w.recursive {
		if len(w.entries) == 0 {
			return "", TreeEntry{}, io.EOF
		}
		e := w.entries[0]
		w.entries = w.entries[1:]
		return e.Name, e, nil
	}
	if !w.init {
		w.init = true
		w.ents, w.err = w.r.walk(w.tree, walkTrees|walkSubmodules)
	}
	if w.err != nil {
		return "", TreeEntry{}, w.err
	}
	if len(w.ents) == 0 {
		return "", TreeEntry{}, io.EOF
	}
	e := w.ents[0]
	w.ents = w.ents[1:]
	return e.path, TreeEntry{Name: path.Base(e.path), Mode: e.mode, Hash: e.hash}, nil
}

// Close releases the walker.
func (w *TreeWalker) Close() { w.ents, w.entries, w.init = nil, nil, true }

// parseEntries decodes a raw tree, appending to dst.
// Names alias raw, and modes are canonical.
func parseEntries(raw string, dst []TreeEntry) ([]TreeEntry, error) {
	for b := raw; len(b) > 0; {
		sp := strings.IndexByte(b, ' ')
		if sp < 0 {
			return dst, fmt.Errorf("%w: missing mode terminator", ErrMalformedTree)
		}
		mode, ok := parseMode(b[:sp])
		if !ok {
			return dst, fmt.Errorf("%w: malformed mode", ErrMalformedTree)
		}
		b = b[sp+1:]
		z := strings.IndexByte(b, 0)
		switch {
		case z < 0:
			return dst, fmt.Errorf("%w: missing filename terminator", ErrMalformedTree)
		case z == 0:
			return dst, fmt.Errorf("%w: empty filename", ErrMalformedTree)
		case z+21 > len(b):
			return dst, fmt.Errorf("%w: truncated object id", ErrMalformedTree)
		}
		e := TreeEntry{Name: b[:z], Mode: mode}
		copy(e.Hash[:], b[z+1:z+21])
		dst = append(dst, e)
		b = b[z+21:]
	}
	return dst, nil
}

// parseMode parses a tree entry's octal mode, as strconv.ParseUint(s, 8, 32)
// does, and returns its canonical form.
func parseMode(s string) (FileMode, bool) {
	if len(s) == 0 {
		return 0, false
	}
	var m uint64
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '7' {
			return 0, false
		}
		if m = m<<3 | uint64(c-'0'); m > 1<<32-1 {
			return 0, false
		}
	}
	return canonicalMode(FileMode(m)), true
}

// canonicalMode returns the mode git reports for an entry with mode m.
// 100664 is a regular file, and an unknown type is a submodule.
func canonicalMode(m FileMode) FileMode {
	switch m & modeTypeMask {
	case Dir:
		return Dir
	case 0o100000: // a file
		if m&0o111 != 0 {
			return Executable
		}
		return Regular
	case Symlink:
		return Symlink
	}
	return Submodule
}
