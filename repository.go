package git2

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"runtime"
	"sync"

	"github.com/msuozzo/go-git2/internal/wasi"
)

// FS is a read-only filesystem containing a repository at its root, either
// a bare repository or a working tree with a .git directory. Names are
// slash-separated and relative, as in io/fs. FromFS adapts an io/fs file
// system, and the separate module github.com/msuozzo/go-git2/billyfs
// adapts a go-billy one.
type FS interface {
	Open(name string) (FSFile, error)
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	Readlink(name string) (string, error)
}

// FSFile is an open file or directory in an FS.
type FSFile = wasi.File

// Repository is an open git repository.
type Repository struct {
	mu      sync.Mutex
	rt      *instance
	repo    int32   // git_repository*
	odb     int32   // git_odb*
	fsys    wasi.FS // the files the module sees, at mount
	closer  io.Closer
	cleanup runtime.Cleanup
	fault   error // set once the instance faults (see unlock)
}

// OpenOptions configures how a repository is opened.
type OpenOptions struct {
	// SkipHashVerification reads objects without checking them against
	// their ids. Reads are faster, but a repository's files can then
	// replace any object's content, so skip it only for trusted
	// repositories.
	SkipHashVerification bool
}

// PlainOpen opens the repository at path: a bare repository, or a working
// tree with a .git directory. Access is confined to path, so a .git file
// (the "gitdir:" pointer that submodule checkouts and linked worktrees
// use) is not followed and PlainOpen returns an error.
func PlainOpen(path string) (*Repository, error) {
	return PlainOpenWithOptions(path, nil)
}

// PlainOpenWithOptions is like PlainOpen but takes options. A nil o means
// the defaults.
func PlainOpenWithOptions(path string, o *OpenOptions) (*Repository, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrRepositoryNotExists
		}
		return nil, err
	}
	r, err := open(impliedRefs{wasi.DirFS{Root: root}}, root, o)
	if err != nil {
		root.Close()
		return nil, err
	}
	return r, nil
}

// Open opens the repository at the root of fsys.
func Open(fsys FS) (*Repository, error) {
	return OpenWithOptions(fsys, nil)
}

// OpenWithOptions is like Open but takes options. A nil o means the
// defaults.
func OpenWithOptions(fsys FS, o *OpenOptions) (*Repository, error) {
	return open(impliedRefs{fsys}, nil, o)
}

func open(fsys wasi.FS, closer io.Closer, o *OpenOptions) (_ *Repository, err error) {
	if fi, err := fsys.Lstat(".git"); err == nil && fi.Mode().IsRegular() {
		return nil, errGitFile
	}
	verify := o == nil || !o.SkipHashVerification
	rt, err := newInstance(fsys, verify)
	if err != nil {
		return nil, err
	}
	// Opening reads the repository's config and HEAD. A fault there is
	// reported the same way as in later calls.
	closed := false
	defer func() {
		if p := recover(); p != nil {
			if !closed {
				rt.close()
			}
			err = newFault(p)
		}
	}()
	p, heap := rt.putStr(mount)
	code := rt.mod.Xg2_open(p, rt.slot(0), rt.slot(1))
	rt.freeStr(p, heap)
	if code < 0 {
		err := rt.error(code)
		rt.close()
		closed = true
		if code == gitENotFound {
			return nil, ErrRepositoryNotExists
		}
		return nil, err
	}
	r := &Repository{
		rt:     rt,
		repo:   int32(rt.slotU32(0)),
		odb:    int32(rt.slotU32(1)),
		fsys:   fsys,
		closer: closer,
	}
	// A Repository that is never closed still returns its memory, which
	// lives outside the Go heap, and its files.
	r.cleanup = runtime.AddCleanup(r, releaseLeaked, leaked{rt, closer})
	return r, nil
}

type leaked struct {
	rt     *instance
	closer io.Closer
}

func releaseLeaked(l leaked) {
	l.rt.close()
	if l.closer != nil {
		l.closer.Close()
	}
}

// Close releases the repository's module instance and open files.
func (r *Repository) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rt == nil {
		return nil
	}
	r.cleanup.Stop()
	var err error
	if r.fault == nil {
		err = r.closeModule()
	}
	r.rt.close()
	r.rt = nil
	if r.closer != nil {
		if cerr := r.closer.Close(); err == nil {
			err = cerr
		}
	}
	return err
}

// closeModule frees the repository inside the module. A fault there is
// returned instead of raised, so Close still releases the instance.
func (r *Repository) closeModule() (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = newFault(p)
		}
	}()
	r.rt.mod.Xg2_close(r.repo, r.odb)
	return nil
}

// lock acquires the instance. It returns ErrClosed after Close, and the
// fault's error after a fault (see unlock).
func (r *Repository) lock() (*instance, error) {
	r.mu.Lock()
	if r.rt == nil || r.fault != nil {
		err := r.fault
		if r.rt == nil {
			err = ErrClosed
		}
		r.mu.Unlock()
		return nil, err
	}
	return r.rt, nil
}

// unlock releases the instance acquired by lock, and is always deferred. A
// panic while the instance is held, from a trap in the module or a bug
// decoding its output, becomes a *FaultError in *err. The instance is then
// retired, because its state can no longer be trusted, and every later call
// fails with the same error.
func (r *Repository) unlock(err *error) {
	if p := recover(); p != nil {
		r.fault = newFault(p)
		*err = r.fault
	}
	r.mu.Unlock()
}

// IsShallow reports whether the repository has a shallow file.
func (r *Repository) IsShallow() (_ bool, err error) {
	rt, err := r.lock()
	if err != nil {
		return false, err
	}
	defer r.unlock(&err)
	code := rt.mod.Xg2_is_shallow(r.repo)
	if code < 0 {
		return false, rt.error(code)
	}
	return code == 1, nil
}

// ---- references ------------------------------------------------------------

func (r *Repository) reference(rt *instance, name ReferenceName, resolve bool) (*Reference, error) {
	if hasNUL(string(name)) {
		return nil, ErrReferenceNotFound
	}
	p, heap := rt.putStr(string(name))
	code := rt.mod.Xg2_ref_lookup(r.repo, p, b2i(resolve), rt.slot(0))
	rt.freeStr(p, heap)
	if code < 0 {
		if code == gitENotFound || code == gitEInvalid {
			return nil, ErrReferenceNotFound
		}
		return nil, rt.error(code)
	}
	ref := int32(rt.slotU32(0))
	defer rt.mod.Xg2_ref_free(ref)
	var b refBuilder
	return b.add(rt, ref), nil
}

// Reference returns the named reference. With resolved, symbolic
// references are followed to the direct reference they point at.
func (r *Repository) Reference(name ReferenceName, resolved bool) (_ *Reference, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	return r.reference(rt, name, resolved)
}

// Head returns the reference HEAD resolves to.
func (r *Repository) Head() (*Reference, error) {
	return r.Reference(HEAD, true)
}

func (r *Repository) refs(glob string) (_ *ReferenceIter, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	var refs []*Reference
	if glob == "" {
		// Like go-git's filesystem storage, include HEAD.
		if h, err := r.reference(rt, HEAD, false); err == nil {
			refs = append(refs, h)
		}
	}
	g, heap := int32(0), false
	if glob != "" {
		g, heap = rt.putStr(glob)
	}
	code := rt.mod.Xg2_ref_iter_new(r.repo, g, rt.slot(0))
	rt.freeStr(g, heap)
	if code < 0 {
		return nil, rt.error(code)
	}
	it := int32(rt.slotU32(0))
	defer rt.mod.Xg2_ref_iter_free(it)
	var b refBuilder
	for {
		code := rt.mod.Xg2_ref_next(it, rt.slot(0))
		if code == gitIterOver {
			return &ReferenceIter{refs: refs}, nil
		}
		if code < 0 {
			return nil, rt.error(code)
		}
		ref := int32(rt.slotU32(0))
		refs = append(refs, b.add(rt, ref))
		rt.mod.Xg2_ref_free(ref)
	}
}

// References returns all references, including HEAD.
func (r *Repository) References() (*ReferenceIter, error) { return r.refs("") }

// Branches returns the local branches (refs/heads/*).
func (r *Repository) Branches() (*ReferenceIter, error) { return r.refs(refHeadPrefix + "*") }

// Tags returns the tag references (refs/tags/*), annotated or not.
func (r *Repository) Tags() (*ReferenceIter, error) { return r.refs(refTagPrefix + "*") }

// Tag returns the reference refs/tags/<name>, or ErrTagNotFound.
func (r *Repository) Tag(name string) (*Reference, error) {
	ref, err := r.Reference(NewTagReferenceName(name), false)
	if err == ErrReferenceNotFound {
		return nil, ErrTagNotFound
	}
	return ref, err
}

// Revision is a revision expression as understood by git rev-parse.
type Revision string

// ResolveRevision resolves rev to a commit. It accepts anything libgit2's
// rev-parse accepts, such as refs, short hashes, ~, ^, @{...} and
// ^{/regex}. Annotated tags are peeled to the commit they point at. A
// revision that does not resolve to a commit returns ErrReferenceNotFound.
func (r *Repository) ResolveRevision(rev Revision) (_ *Hash, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	if hasNUL(string(rev)) {
		return nil, ErrReferenceNotFound
	}
	p, heap := rt.putStr(string(rev))
	code := rt.mod.Xg2_revparse(r.repo, p, rt.slot(0))
	rt.freeStr(p, heap)
	if code < 0 {
		if code == gitENotFound || code == gitEInvalid || code == gitEAmbiguous {
			return nil, ErrReferenceNotFound
		}
		return nil, rt.error(code)
	}
	h := rt.hashAt(uint32(rt.slot(0)))
	code = rt.mod.Xg2_peel(r.repo, rt.putOID(oidA, h), objCommit, rt.slot(0))
	if code < 0 {
		// GIT_EINVALIDSPEC means the object is a tree or blob, which can't
		// be peeled to a commit.
		if code == gitENotFound || code == gitEPeel || code == gitEInvalid {
			return nil, ErrReferenceNotFound
		}
		return nil, rt.error(code)
	}
	h = rt.hashAt(uint32(rt.slot(0)))
	return &h, nil
}

// ---- objects ---------------------------------------------------------------

// ObjectType is the type of a git object. It converts directly to go-git's
// plumbing.ObjectType.
type ObjectType int8

const (
	InvalidObject ObjectType = 0
	CommitObject  ObjectType = 1
	TreeObject    ObjectType = 2
	BlobObject    ObjectType = 3
	TagObject     ObjectType = 4
	AnyObject     ObjectType = -127
)

func (t ObjectType) String() string {
	switch t {
	case CommitObject:
		return "commit"
	case TreeObject:
		return "tree"
	case BlobObject:
		return "blob"
	case TagObject:
		return "tag"
	case AnyObject:
		return "any"
	}
	return "unknown"
}

func objectNotFound(rt *instance, code int32) error {
	if code == gitENotFound {
		return ErrObjectNotFound
	}
	return rt.error(code)
}

// HasObject reports whether the object database contains h.
func (r *Repository) HasObject(h Hash) (_ bool, err error) {
	rt, err := r.lock()
	if err != nil {
		return false, err
	}
	defer r.unlock(&err)
	return rt.mod.Xg2_odb_exists(r.odb, rt.putOID(oidA, h)) == 1, nil
}

// ObjectHeader returns the type and size of object h without reading it.
func (r *Repository) ObjectHeader(h Hash) (_ ObjectType, _ int64, err error) {
	rt, err := r.lock()
	if err != nil {
		return InvalidObject, 0, err
	}
	defer r.unlock(&err)
	return r.header(rt, h)
}

func (r *Repository) header(rt *instance, h Hash) (ObjectType, int64, error) {
	code := rt.mod.Xg2_odb_read_header(r.odb, rt.putOID(oidA, h), rt.slot(0), rt.slot(1))
	if code < 0 {
		return InvalidObject, 0, objectNotFound(rt, code)
	}
	return ObjectType(int32(rt.slotU32(1))), int64(rt.slotU32(0)), nil
}

// readRaw reads object h, appending its contents to dst. If want is not
// AnyObject, objects of other types yield ErrObjectNotFound.
func (r *Repository) readRaw(rt *instance, h Hash, want ObjectType, dst []byte) ([]byte, ObjectType, error) {
	b, obj, t, err := r.lookupObject(rt, h, want)
	if err != nil {
		return dst, t, err
	}
	dst = append(dst, b...)
	rt.mod.Xg2_odb_object_free(obj)
	return dst, t, nil
}

// readString reads object h into a new string, as readRaw does.
func (r *Repository) readString(rt *instance, h Hash, want ObjectType) (string, ObjectType, error) {
	b, obj, t, err := r.lookupObject(rt, h, want)
	if err != nil {
		return "", t, err
	}
	s := string(b)
	rt.mod.Xg2_odb_object_free(obj)
	return s, t, nil
}

// lookupObject reads object h into module memory. Unless it fails, the
// caller copies the contents out of b and then frees obj.
func (r *Repository) lookupObject(rt *instance, h Hash, want ObjectType) (b []byte, obj int32, t ObjectType, err error) {
	code := rt.mod.Xg2_odb_read(r.odb, rt.putOID(oidA, h), rt.slot(0), rt.slot(1), rt.slot(2), rt.slot(3))
	if code < 0 {
		return nil, 0, InvalidObject, objectNotFound(rt, code)
	}
	obj = int32(rt.slotU32(0))
	t = ObjectType(int32(rt.slotU32(3)))
	if want != AnyObject && t != want {
		rt.mod.Xg2_odb_object_free(obj)
		return nil, 0, t, ErrObjectNotFound
	}
	return rt.bytes(rt.slotU32(1), rt.slotU32(2)), obj, t, nil
}

// CommitObject returns the commit with hash h, or ErrObjectNotFound.
func (r *Repository) CommitObject(h Hash) (_ *Commit, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	return r.commit(rt, h)
}

func (r *Repository) commit(rt *instance, h Hash) (*Commit, error) {
	raw, _, err := r.readString(rt, h, CommitObject)
	if err != nil {
		return nil, err
	}
	return decodeCommit(r, h, raw)
}

// TreeObject returns the tree with hash h, or ErrObjectNotFound.
func (r *Repository) TreeObject(h Hash) (_ *Tree, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	return r.tree(rt, h)
}

func (r *Repository) tree(rt *instance, h Hash) (*Tree, error) {
	raw, _, err := r.readString(rt, h, TreeObject)
	if err != nil {
		return nil, err
	}
	t := &Tree{Hash: h, r: r}
	if err := t.decode(raw); err != nil {
		return nil, err
	}
	return t, nil
}

// BlobObject returns the blob with hash h, or ErrObjectNotFound. Its
// contents are read on demand.
func (r *Repository) BlobObject(h Hash) (_ *Blob, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	t, size, err := r.header(rt, h)
	if err != nil {
		return nil, err
	}
	if t != BlobObject {
		return nil, ErrObjectNotFound
	}
	return &Blob{Hash: h, Size: size, r: r}, nil
}

// TagObject returns the annotated tag object with hash h, or
// ErrObjectNotFound. Lightweight tags have no tag object, so their hashes
// also return ErrObjectNotFound.
func (r *Repository) TagObject(h Hash) (_ *Tag, err error) {
	rt, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer r.unlock(&err)
	raw, _, err := r.readString(rt, h, TagObject)
	if err != nil {
		return nil, err
	}
	t := &Tag{Hash: h, r: r}
	if err := t.decode(raw); err != nil {
		return nil, err
	}
	return t, nil
}

// ReadBlob appends the contents of blob h to dst. With a reused dst it
// performs no allocations.
func (r *Repository) ReadBlob(h Hash, dst []byte) (_ []byte, err error) {
	rt, err := r.lock()
	if err != nil {
		return dst, err
	}
	defer r.unlock(&err)
	return r.readBlob(rt, h, dst)
}

func (r *Repository) readBlob(rt *instance, h Hash, dst []byte) ([]byte, error) {
	b, blob, err := r.lookupBlob(rt, h)
	if err != nil {
		return dst, err
	}
	dst = append(dst, b...)
	rt.mod.Xg2_blob_free(blob)
	return dst, nil
}

// blobString returns the contents of blob h.
func (r *Repository) blobString(h Hash) (_ string, err error) {
	rt, err := r.lock()
	if err != nil {
		return "", err
	}
	defer r.unlock(&err)
	b, blob, err := r.lookupBlob(rt, h)
	if err != nil {
		return "", err
	}
	s := string(b)
	rt.mod.Xg2_blob_free(blob)
	return s, nil
}

// lookupBlob reads blob h into module memory. Unless it fails, the caller
// copies the contents out of b and then frees blob.
func (r *Repository) lookupBlob(rt *instance, h Hash) (b []byte, blob int32, err error) {
	code := rt.mod.Xg2_blob_lookup(r.repo, rt.putOID(oidA, h), rt.slot(0), rt.slot(1), rt.slot(2))
	if code < 0 {
		return nil, 0, objectNotFound(rt, code)
	}
	return rt.bytes(rt.slotU32(1), rt.slotU32(2)), int32(rt.slotU32(0)), nil
}

// entryByPath resolves a slash-separated path under tree.
func (r *Repository) entryByPath(rt *instance, tree Hash, path string) (FileMode, Hash, error) {
	if hasNUL(path) {
		return 0, ZeroHash, ErrObjectNotFound
	}
	t := rt.putOID(oidA, tree)
	p, heap := rt.putStr(path)
	code := rt.mod.Xg2_tree_entry_bypath(r.repo, t, p, rt.slot(0), rt.slot(1))
	rt.freeStr(p, heap)
	if code < 0 {
		return 0, ZeroHash, objectNotFound(rt, code)
	}
	return canonicalMode(FileMode(rt.slotU32(0))), rt.hashAt(uint32(rt.slot(1))), nil
}
