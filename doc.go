// Package git2 is a git library backed by libgit2, compiled to
// WebAssembly and translated to pure Go with wasm2go. It needs no cgo. For
// now it only reads repositories, and it runs on 64-bit Linux, macOS and
// Windows.
//
// The API is modeled on go-git v5 (github.com/go-git/go-git/v5). Its types,
// fields, options and error sentinels keep go-git's names, so most code that
// only reads from a repository can switch by changing imports. Where go-git
// and git report different things about a repository, go-git2 reports what
// git does. The differences are listed below, and the identifiers involved
// note them too.
//
// Each Repository owns an instance of the module, with its own linear
// memory and libgit2 caches. Its methods are safe for concurrent use but
// run one at a time, so open several Repositories to read in parallel.
//
// # Differences from go-git
//
// These change what a read returns:
//
//   - History filtering (LogOptions.PathFilter and FileName) compares each
//     commit with its parents, selecting the commits git log -- <path> does,
//     except that side branches are never pruned. go-git compares each commit
//     with the next one in iteration order, which is often not a parent, so
//     it reports commits that don't touch the path. On google-cloud-python,
//     go-git returns 271 commits for a file that git and go-git2 agree is
//     changed by 47.
//   - Log with All is a single walk across every reference. go-git combines
//     a walk per reference and misses commits that only some references
//     reach.
//   - In LogOrderCommitterTime, Since ends the walk at the first commit older
//     than it, as git log does, where go-git filters every commit. The
//     results differ only when clock skew puts a newer commit behind an older
//     one.
//   - Shallow clones are walked up to the shallow boundary instead of failing
//     on the missing parents.
//   - Time zones such as -0030 keep their sign. go-git reads them as +0030.
//   - Tree entry names are returned as stored. Since v5.19.1, go-git rejects
//     entries that git refuses to check out, such as .git, .. or names
//     containing control characters. Check names before using them as
//     filesystem paths.
//
// These change how code is written:
//
//   - Types from go-git's object, plumbing and filemode packages are in this
//     one package.
//   - Repositories open from a path or a filesystem, not a go-git storer, and
//     hold module state until Close.
//   - A PathFilter runs while the walk holds the Repository, so it must not
//     call the Repository's methods.
//   - File does not embed a Blob. Tree walks read only trees, and a file's
//     contents and size are loaded when requested (Contents, Bytes, Size).
//   - Tree.FindEntry returns ErrEntryNotFound for a missing intermediate
//     directory, where go-git returns ErrDirectoryNotFound.
//   - NewTreeWalker takes no seen map.
package git2
