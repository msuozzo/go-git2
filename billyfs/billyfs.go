// Package billyfs lets git2 read a repository from a go-billy filesystem.
// For example, a repository that go-git cloned into a memfs can be opened
// with git2.Open(billyfs.New(fs)), without first writing it to disk.
//
// For a repository on disk, git2.PlainOpen confines access to its
// directory. Over osfs, use osfs.New(dir, osfs.WithBoundOS()), because the
// default follows symbolic links out of the directory.
package billyfs

import (
	"io/fs"

	"github.com/go-git/go-billy/v5"

	"github.com/msuozzo/go-git2"
)

// New returns fsys as a git2.FS rooted at fsys's root. Use
// billy.Filesystem.Chroot to point it at a repository below the root.
func New(fsys billy.Filesystem) git2.FS { return billyFS{fsys} }

type billyFS struct{ fs billy.Filesystem }

func (b billyFS) Open(name string) (git2.FSFile, error) {
	fi, err := b.fs.Stat(name)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return &dir{fs: b.fs, name: name, fi: fi}, nil
	}
	f, err := b.fs.Open(name)
	if err != nil {
		return nil, err
	}
	// billy files cannot stat themselves. The repository is read-only, so
	// the size seen at open stays valid.
	return &file{File: f, fi: fi}, nil
}

func (b billyFS) Stat(name string) (fs.FileInfo, error)  { return b.fs.Stat(name) }
func (b billyFS) Lstat(name string) (fs.FileInfo, error) { return b.fs.Lstat(name) }
func (b billyFS) Readlink(name string) (string, error)   { return b.fs.Readlink(name) }

type file struct {
	billy.File
	fi fs.FileInfo
}

func (f *file) Stat() (fs.FileInfo, error)         { return f.fi, nil }
func (f *file) ReadDir(int) ([]fs.DirEntry, error) { return nil, fs.ErrInvalid }

type dir struct {
	fs   billy.Filesystem
	name string
	fi   fs.FileInfo
}

func (d *dir) Read([]byte) (int, error)          { return 0, fs.ErrInvalid }
func (d *dir) ReadAt([]byte, int64) (int, error) { return 0, fs.ErrInvalid }
func (d *dir) Seek(int64, int) (int64, error)    { return 0, fs.ErrInvalid }
func (d *dir) Close() error                      { return nil }
func (d *dir) Stat() (fs.FileInfo, error)        { return d.fi, nil }

// ReadDir returns the whole directory whatever n is, which is enough because
// the WASI host reads a directory with a single ReadDir(-1).
func (d *dir) ReadDir(int) ([]fs.DirEntry, error) {
	infos, err := d.fs.ReadDir(d.name)
	if err != nil {
		return nil, err
	}
	ents := make([]fs.DirEntry, len(infos))
	for i, fi := range infos {
		ents[i] = fs.FileInfoToDirEntry(fi)
	}
	return ents, nil
}
