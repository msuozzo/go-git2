package git2

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"time"
)

// FromFS adapts an io/fs file system to FS. Regular files must implement
// io.ReaderAt and io.Seeker, as files from os.DirFS, os.Root.FS and
// testing/fstest.MapFS do. Symbolic links are supported when fsys
// implements fs.ReadLinkFS. For a directory on disk, prefer os.Root.FS to
// os.DirFS: like PlainOpen, it keeps symbolic links from reaching outside
// the directory.
func FromFS(fsys fs.FS) FS { return ioFS{fsys} }

type ioFS struct{ fsys fs.FS }

var errNotSeekable = errors.New("git2: file does not implement io.ReaderAt and io.Seeker")

func (f ioFS) Open(name string) (FSFile, error) {
	file, err := f.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	fi, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if fi.IsDir() {
		return &ioDir{File: file, fsys: f.fsys, name: name}, nil
	}
	rf, ok := file.(seekableFile)
	if !ok {
		file.Close()
		return nil, &fs.PathError{Op: "open", Path: name, Err: errNotSeekable}
	}
	return ioFile{rf}, nil
}

func (f ioFS) Stat(name string) (fs.FileInfo, error) { return fs.Stat(f.fsys, name) }

func (f ioFS) Lstat(name string) (fs.FileInfo, error) {
	if l, ok := f.fsys.(fs.ReadLinkFS); ok {
		return l.Lstat(name)
	}
	return fs.Stat(f.fsys, name)
}

func (f ioFS) Readlink(name string) (string, error) {
	if l, ok := f.fsys.(fs.ReadLinkFS); ok {
		return l.ReadLink(name)
	}
	return "", &fs.PathError{Op: "readlink", Path: name, Err: fs.ErrInvalid}
}

type seekableFile interface {
	fs.File
	io.ReaderAt
	io.Seeker
}

type ioFile struct{ seekableFile }

func (ioFile) ReadDir(int) ([]fs.DirEntry, error) { return nil, fs.ErrInvalid }

// ioDir is an open directory. Reads and seeks fail, as they do for an
// os.File directory. ReadDir ignores n and returns the whole directory,
// which is enough because the WASI host reads each directory with a single
// ReadDir(-1).
type ioDir struct {
	fs.File
	fsys fs.FS
	name string
}

func (d *ioDir) ReadAt([]byte, int64) (int, error)    { return 0, fs.ErrInvalid }
func (d *ioDir) Seek(int64, int) (int64, error)       { return 0, fs.ErrInvalid }
func (d *ioDir) ReadDir(n int) ([]fs.DirEntry, error) { return fs.ReadDir(d.fsys, d.name) }

// impliedRefs presents a missing refs directory as an empty one. git always
// creates it, and libgit2 requires it, but a copy of a repository whose
// refs are all packed may lack it. go-git can read such copies, so this
// package can too.
type impliedRefs struct{ FS }

func isRefsDir(name string) bool { return name == "refs" || name == ".git/refs" }

func (f impliedRefs) Open(name string) (FSFile, error) {
	file, err := f.FS.Open(name)
	if err != nil && isRefsDir(name) && errors.Is(err, fs.ErrNotExist) {
		return emptyDir{name}, nil
	}
	return file, err
}

func (f impliedRefs) Stat(name string) (fs.FileInfo, error) {
	fi, err := f.FS.Stat(name)
	if err != nil && isRefsDir(name) && errors.Is(err, fs.ErrNotExist) {
		return emptyDir{name}, nil
	}
	return fi, err
}

func (f impliedRefs) Lstat(name string) (fs.FileInfo, error) {
	fi, err := f.FS.Lstat(name)
	if err != nil && isRefsDir(name) && errors.Is(err, fs.ErrNotExist) {
		return emptyDir{name}, nil
	}
	return fi, err
}

// emptyDir is both an open empty directory and its FileInfo. Like ioDir's,
// its ReadDir ignores n.
type emptyDir struct{ name string }

func (d emptyDir) Name() string                       { return path.Base(d.name) }
func (d emptyDir) Size() int64                        { return 0 }
func (d emptyDir) Mode() fs.FileMode                  { return fs.ModeDir | 0o755 }
func (d emptyDir) ModTime() time.Time                 { return time.Time{} }
func (d emptyDir) IsDir() bool                        { return true }
func (d emptyDir) Sys() any                           { return nil }
func (d emptyDir) Stat() (fs.FileInfo, error)         { return d, nil }
func (d emptyDir) Read([]byte) (int, error)           { return 0, fs.ErrInvalid }
func (d emptyDir) ReadAt([]byte, int64) (int, error)  { return 0, fs.ErrInvalid }
func (d emptyDir) Seek(int64, int) (int64, error)     { return 0, fs.ErrInvalid }
func (d emptyDir) Close() error                       { return nil }
func (d emptyDir) ReadDir(int) ([]fs.DirEntry, error) { return nil, nil }
