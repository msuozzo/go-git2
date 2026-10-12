# go-git2

[![Go Reference](https://pkg.go.dev/badge/github.com/msuozzo/go-git2.svg)](https://pkg.go.dev/github.com/msuozzo/go-git2)
[![CI](https://github.com/msuozzo/go-git2/actions/workflows/ci.yml/badge.svg)](https://github.com/msuozzo/go-git2/actions/workflows/ci.yml)

> [!IMPORTANT]
> go-git2 only supports read operations for now. It can't clone, fetch,
> check out or commit.

go-git2 is a Go library for interacting with git repositories. It is
built on [libgit2](https://libgit2.org) and modeled on
[go-git](https://github.com/go-git/go-git)'s API.

- **Fast.** Often *10–100×* faster than go-git, with speeds close to the
  git CLI itself. See [Performance](#performance).
- **Pure Go.** libgit2 is compiled to WebAssembly and translated to Go
  source with [wasm2go](https://github.com/ncruces/wasm2go), so go-git2
  builds with the standard Go toolchain and `CGO_ENABLED=0`. It needs no C
  compiler and no Go modules beyond its own.
- **Memory-safe.** libgit2 runs inside a sandbox, so a memory bug can't
  corrupt your program's memory or take over its execution. See
  [Safety](#safety).
- **Familiar API.** A near drop-in replacement for go-git, with results
  more faithful to the git CLI. See [Compatibility](#compatibility).

## Installation

```sh
go get github.com/msuozzo/go-git2
```

go-git2 requires Go 1.25 or later on 64-bit Linux, macOS or Windows.
32-bit platforms aren't supported yet. Its only dependency is its own
`libgit2` module.

## Usage

```go
r, err := git2.PlainOpen("path/to/repo")
if err != nil {
	log.Fatal(err)
}
defer r.Close()

// Every commit, on any branch, that changed go.mod, newest first.
path := "go.mod"
commits, err := r.Log(&git2.LogOptions{
	All:      true,
	Order:    git2.LogOrderCommitterTime,
	FileName: &path,
})
if err != nil {
	log.Fatal(err)
}
err = commits.ForEach(func(c *git2.Commit) error {
	fmt.Println(c.Hash, c.Committer.When)
	return nil
})
```

`PlainOpen` reads a repository from disk. `Open` reads one from another
filesystem, such as an `io/fs.FS` through `git2.FromFS` or a
[go-billy](https://github.com/go-git/go-billy) filesystem through
`billyfs.New`, from the separate module `github.com/msuozzo/go-git2/billyfs`.
The [package documentation](https://pkg.go.dev/github.com/msuozzo/go-git2)
has more examples.

## Performance

go-git2's biggest wins are history queries, which go-git answers by
walking all of history and diffing every commit's tree. These are times for
go-git v5.19.3, go-git2 and the git CLI on
[django](https://github.com/django/django) (35k commits, 7k files):

| Workload | go-git | go-git2 | Speedup | | git CLI¹ |
| --- | ---: | ---: | ---: | --- | ---: |
| Commits that changed a file, over full history | 39.7s | 352ms | 113× | | 255ms² |
| Commits from the last 90 days | 382ms | 4ms | 96× | | 6ms |
| List the files at `HEAD` | 263ms | 8ms | 32× | | 10ms |
| Open the repository and read the `HEAD` commit | 39ms | 2ms | 16× | | 5ms |

The [benchmark results](bench/README.md) cover every workload on
repositories of various sizes, with allocations and how to reproduce them.

¹ git 2.55.0, timed including process startup. The repository has no
commit-graph file, as after a fresh clone. git would use one to speed up
its walks.

² With `--full-history`, since go-git2 doesn't prune side branches as git
does by default.

## Compatibility

Read-only code usually needs new imports and a call to `Close`. Importing
go-git2 as `git` keeps most call sites as they are:

```diff
 import (
-	"github.com/go-git/go-git/v5"
-	"github.com/go-git/go-git/v5/plumbing/object"
+	git "github.com/msuozzo/go-git2"
 )

 r, err := git.PlainOpen(path)
 if err != nil {
 	return err
 }
+defer r.Close()

 since := time.Now().AddDate(0, 0, -90)
 commits, err := r.Log(&git.LogOptions{
 	All:        true,
 	Order:      git.LogOrderCommitterTime,
 	Since:      &since,
 	PathFilter: func(p string) bool { return strings.HasPrefix(p, "django/db/") },
 })
 if err != nil {
 	return err
 }
-err = commits.ForEach(func(c *object.Commit) error {
+err = commits.ForEach(func(c *git.Commit) error {
 	fmt.Println(c.Hash, c.Committer.When)
 	return nil
 })
```

> [!NOTE]
> On django, this query drops from **43s** with go-git to **under 50ms**
> with go-git2.

go-git2 is a single package, so types from go-git's `object`, `plumbing`
and `filemode` packages, such as `object.Commit` and `plumbing.Hash`,
become `git.Commit` and `git.Hash`. Repositories open from a path or a
filesystem rather than from a go-git storer.

### Differences from go-git

- **Path filters compare each commit with its parents**, as
  `git log -- <path>` does. go-git compares it with the next commit it
  visits, which is often not a parent.
- **`Log` with `All` reaches every commit** on every reference. go-git can
  miss commits that only some references reach.
- **Tree entry names are returned as stored**, including ones git refuses
  to check out, such as `.git` or `..`. Check them before using them as
  paths.

The [package documentation](https://pkg.go.dev/github.com/msuozzo/go-git2#hdr-Differences_from_go_git)
lists every difference.

### Not yet supported

Writing (clone, fetch, checkout, commit), iterating the whole object
database (`CommitObjects`, `TreeObjects`, `BlobObjects`), `DiffTree` and
`Patch`, the `LogOrderDFSPost` and `LogOrderBSF` orders, the `seen` argument
of `NewTreeWalker`, `Commit.ExtraHeaders`, submodule checkouts and linked
worktrees (whose `.git` is a file), reftable, SHA-256 repositories and 32-bit
platforms.

## How it works

go-git2 is [libgit2](https://libgit2.org) compiled to WebAssembly and
translated to Go source by [wasm2go](https://github.com/ncruces/wasm2go).
History walks, tree walks and object parsing are written in Go on top of
it, and a small read-only WASI host gives libgit2 the repository's files.

Each `Repository` runs its own instance of the module, so its methods run
one at a time. Open several to read in parallel.

## Safety

go-git2 doesn't link libgit2's C code into your program. libgit2 is
compiled to WebAssembly and translated to Go, which confines it:

- Its heap, stack and globals are one byte array, and every memory access
  it makes is bounds-checked against that array. A memory bug in libgit2
  can only interfere with libgit2's own data.
- It can't run code in your program. Every call it makes goes to one of
  its own functions, fixed at build time.
- It sees only the files go-git2 gives it, read-only and within the
  repository, and it has no network access.
- If it crashes, go-git2 returns an error wrapping `ErrFault` and discards
  that repository's state.

The sandbox has limits:

- libgit2's bugs are contained, not removed. One can still make go-git2
  return wrong data for that repository. Hash verification, on by default,
  catches corrupted or tampered objects as they're read.
- An operation can hang or use a lot of memory, up to the sandbox's 4GiB
  limit.
- The guarantee rests on wasm2go's translation and on the Go runtime.
  go-git2's own code doesn't use `unsafe`. go-git2 serializes access to
  the sandbox, but simultaneous access isn't supported yet.

## Versioning

go-git2 is three modules, released separately. The root module holds the
API, `libgit2/` holds the build and the generated code, and `billyfs/`
holds the go-billy adapter. A `libgit2` version records the libgit2
release it was built from. Release policy and mechanics are in
[RELEASING.md](RELEASING.md).

## Regenerating

The `libgit2` module's generated code is built in Docker from the pins in
`libgit2/build/Dockerfile`:

```sh
cd libgit2 && go generate ./...
```

CI regenerates it on each change and fails if any generated byte differs
from what is committed. [libgit2/build](libgit2/build/README.md) describes
the build.

## License

Code outside `libgit2/` is licensed under the [MIT license](LICENSE). The
`libgit2` module contains libgit2 translated to Go and, like libgit2, is
licensed under the GNU GPL v2 with a linking exception
([libgit2/LICENSE](libgit2/LICENSE)). It also contains wasi-libc
([libgit2/LICENSE.wasi-libc](libgit2/LICENSE.wasi-libc)). Binaries that use
go-git2 embed these license notices, and `Licenses` returns them.
