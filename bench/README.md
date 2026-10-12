# Benchmarks

These benchmarks compare go-git2 with go-git v5.19.3 on read-only workloads
modeled on real git readers, such as finding the commits that changed a
package's version, listing files, searching history by path and reading
blobs.

## Repositories

Each repository is pinned to a commit of its upstream history by
[`repos.go`](repos.go) and built with git 2.55.0. A rebuild restores each
default branch exactly. Other branches and tags are rewound to the same
date, but branches deleted upstream since cannot come back, so results over
every reference are only approximately repeatable.

| Repository | Size | Pinned commit | Commits on the default branch | Commits on any branch | References | Files at `HEAD` | Objects | Pack |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| [celery](https://github.com/celery/celery) | small | `c38600d4` (2026-03-26) | 13,038 | 14,189 | 307 | 820 | 101,962 | 23MiB |
| [django](https://github.com/django/django) | medium | `62ef9d40` (2026-10-06) | 34,979 | 52,906 | 524 | 7,084 | 567,468 | 173MiB |
| [google-cloud-python](https://github.com/googleapis/google-cloud-python) | large | `05b37756` (2026-10-10) | 63,703 | 70,808 | 7,620 | 58,643 | 1,291,319 | 629MiB |

## Results

These ran on an Apple M3 with Go 1.27. Each cold benchmark opens the
repository and runs its workload once, and each row is the median of three
rounds. go-git's times for opening a repository and listing its files
varied between rounds, so those rows take the median of twelve on medium
and large. go-git uses filesystem storage with its default object cache.
Neither library verifies object hashes in these runs. go-git2 verifies them
by default, which adds 0–93% to these workloads on the large repository,
and about 30% to most.

The git CLI column is git 2.55.0 doing the same work, timed including
process startup. Path-filtered history runs with `--full-history`, since
go-git2 doesn't prune side branches as git does by default. These
repositories have no commit-graph file, as after a fresh clone.

The `FileName` rows show only go-git2. go-git's `FileName` is a path filter
like its `PathFilter`, and takes as long.

### small

<table>
<tr><th rowspan="2">Workload</th><th colspan="2">go-git</th><th colspan="2">go-git2</th><th>git CLI</th></tr>
<tr><th>Time</th><th>Allocated</th><th>Time</th><th>Allocated</th><th>Time</th></tr>
<tr><td>Open, resolve <code>HEAD</code> and read the commit</td><td align="right">8ms</td><td align="right">8.58MB</td><td align="right">1ms</td><td align="right">31.7KB</td><td align="right">7ms</td></tr>
<tr><td>Find the commits that changed the package's version, from <code>HEAD</code></td><td align="right">3.67s</td><td align="right">4.46GB</td><td align="right">137ms</td><td align="right">2.66MB</td><td align="right"></td></tr>
<tr><td>The same over all references, in committer-time order</td><td align="right">3.97s</td><td align="right">4.78GB</td><td align="right">149ms</td><td align="right">3.69MB</td><td align="right"></td></tr>
<tr><td><code>Log</code> with a <code>PathFilter</code> over full history</td><td align="right">3.5s</td><td align="right">4.18GB</td><td align="right">245ms</td><td align="right">178MB</td><td align="right">93ms</td></tr>
<tr><td>The same with <code>FileName</code></td><td align="right"></td><td align="right"></td><td align="right">125ms</td><td align="right">3.68MB</td><td align="right"></td></tr>
<tr><td><code>Log</code> from <code>HEAD</code> over every commit</td><td align="right">131ms</td><td align="right">53.1MB</td><td align="right">75ms</td><td align="right">14.3MB</td><td align="right"></td></tr>
<tr><td>Commits from <code>HEAD</code> of the last 90 days</td><td align="right">124ms</td><td align="right">52.6MB</td><td align="right">2ms</td><td align="right">783KB</td><td align="right">5ms</td></tr>
<tr><td>List the file names at <code>HEAD</code></td><td align="right">48ms</td><td align="right">16.2MB</td><td align="right">1ms</td><td align="right">200KB</td><td align="right">5ms</td></tr>
<tr><td><code>Grep</code> for files by name</td><td align="right">32ms</td><td align="right">16.5MB</td><td align="right">2ms</td><td align="right">159KB</td><td align="right"></td></tr>
<tr><td>Find a tag by pattern and peel it</td><td align="right">7ms</td><td align="right">8.84MB</td><td align="right">1ms</td><td align="right">78.9KB</td><td align="right"></td></tr>
<tr><td>Resolve every tag and pick the highest version</td><td align="right">23ms</td><td align="right">22.1MB</td><td align="right">9ms</td><td align="right">478KB</td><td align="right"></td></tr>
<tr><td>Recent commits, on any branch, that touch the package's directory</td><td align="right">3.97s</td><td align="right">4.77GB</td><td align="right">9ms</td><td align="right">2.56MB</td><td align="right">8ms</td></tr>
<tr><td>The same with <code>FileName</code></td><td align="right"></td><td align="right"></td><td align="right">6ms</td><td align="right">657KB</td><td align="right"></td></tr>
<tr><td><code>FindEntry</code> and a blob read in each of 3,000 commits</td><td align="right">90ms</td><td align="right">66.6MB</td><td align="right">40ms</td><td align="right">16.4MB</td><td align="right"></td></tr>
<tr><td>Every commit reachable from any reference</td><td align="right">148ms</td><td align="right">60.8MB</td><td align="right">82ms</td><td align="right">15.6MB</td><td align="right"></td></tr>
<tr><td>Read every blob at <code>HEAD</code></td><td align="right">71ms</td><td align="right">33MB</td><td align="right">25ms</td><td align="right">652KB</td><td align="right"></td></tr>
<tr><td>Warm: resolve <code>HEAD</code> and read the commit</td><td align="right">0.034ms</td><td align="right">26.7KB</td><td align="right">0.030ms</td><td align="right">4.14KB</td><td align="right"></td></tr>
<tr><td>Warm: read 10 files from a loaded tree</td><td align="right">0.021ms</td><td align="right">218KB</td><td align="right">0.005ms</td><td align="right">48.7KB</td><td align="right"></td></tr>
</table>

### medium

<table>
<tr><th rowspan="2">Workload</th><th colspan="2">go-git</th><th colspan="2">go-git2</th><th>git CLI</th></tr>
<tr><th>Time</th><th>Allocated</th><th>Time</th><th>Allocated</th><th>Time</th></tr>
<tr><td>Open, resolve <code>HEAD</code> and read the commit</td><td align="right">39ms</td><td align="right">59.7MB</td><td align="right">2ms</td><td align="right">28.8KB</td><td align="right">5ms</td></tr>
<tr><td>Find the commits that changed the package's version, from <code>HEAD</code></td><td align="right">39.4s</td><td align="right">45.9GB</td><td align="right">379ms</td><td align="right">3.58MB</td><td align="right"></td></tr>
<tr><td>The same over all references, in committer-time order</td><td align="right">42.5s</td><td align="right">49.3GB</td><td align="right">579ms</td><td align="right">5.7MB</td><td align="right"></td></tr>
<tr><td><code>Log</code> with a <code>PathFilter</code> over full history</td><td align="right">39.7s</td><td align="right">46.3GB</td><td align="right">2.01s</td><td align="right">1.56GB</td><td align="right">255ms</td></tr>
<tr><td>The same with <code>FileName</code></td><td align="right"></td><td align="right"></td><td align="right">352ms</td><td align="right">11.6MB</td><td align="right"></td></tr>
<tr><td><code>Log</code> from <code>HEAD</code> over every commit</td><td align="right">388ms</td><td align="right">175MB</td><td align="right">218ms</td><td align="right">39.4MB</td><td align="right"></td></tr>
<tr><td>Commits from <code>HEAD</code> of the last 90 days</td><td align="right">382ms</td><td align="right">171MB</td><td align="right">4ms</td><td align="right">771KB</td><td align="right">6ms</td></tr>
<tr><td>List the file names at <code>HEAD</code></td><td align="right">263ms</td><td align="right">116MB</td><td align="right">8ms</td><td align="right">2.11MB</td><td align="right">10ms</td></tr>
<tr><td><code>Grep</code> for files by name</td><td align="right">274ms</td><td align="right">116MB</td><td align="right">16ms</td><td align="right">1.77MB</td><td align="right"></td></tr>
<tr><td>Find a tag by pattern and peel it</td><td align="right">39ms</td><td align="right">60.2MB</td><td align="right">3ms</td><td align="right">111KB</td><td align="right"></td></tr>
<tr><td>Resolve every tag and pick the highest version</td><td align="right">90ms</td><td align="right">102MB</td><td align="right">22ms</td><td align="right">817KB</td><td align="right"></td></tr>
<tr><td>Recent commits, on any branch, that touch the package's directory</td><td align="right">42.8s</td><td align="right">49.3GB</td><td align="right">42ms</td><td align="right">29.3MB</td><td align="right">14ms</td></tr>
<tr><td>The same with <code>FileName</code></td><td align="right"></td><td align="right"></td><td align="right">16ms</td><td align="right">569KB</td><td align="right"></td></tr>
<tr><td><code>FindEntry</code> and a blob read in each of 3,000 commits</td><td align="right">124ms</td><td align="right">107MB</td><td align="right">40ms</td><td align="right">11.9MB</td><td align="right"></td></tr>
<tr><td>Every commit reachable from any reference</td><td align="right">460ms</td><td align="right">198MB</td><td align="right">342ms</td><td align="right">53.2MB</td><td align="right"></td></tr>
<tr><td>Read every blob at <code>HEAD</code></td><td align="right">597ms</td><td align="right">235MB</td><td align="right">199ms</td><td align="right">4.59MB</td><td align="right"></td></tr>
<tr><td>Warm: resolve <code>HEAD</code> and read the commit</td><td align="right">0.031ms</td><td align="right">14.4KB</td><td align="right">0.030ms</td><td align="right">2.98KB</td><td align="right"></td></tr>
<tr><td>Warm: read 10 files from a loaded tree</td><td align="right">0.017ms</td><td align="right">240KB</td><td align="right">0.005ms</td><td align="right">61.6KB</td><td align="right"></td></tr>
</table>

### large

<table>
<tr><th rowspan="2">Workload</th><th colspan="2">go-git</th><th colspan="2">go-git2</th><th>git CLI</th></tr>
<tr><th>Time</th><th>Allocated</th><th>Time</th><th>Allocated</th><th>Time</th></tr>
<tr><td>Open, resolve <code>HEAD</code> and read the commit</td><td align="right">98ms</td><td align="right">122MB</td><td align="right">5ms</td><td align="right">31.4KB</td><td align="right">6ms</td></tr>
<tr><td>Find the commits that changed the package's version, from <code>HEAD</code></td><td align="right">123s</td><td align="right">193GB</td><td align="right">769ms</td><td align="right">9.98MB</td><td align="right"></td></tr>
<tr><td>The same over all references, in committer-time order</td><td align="right">143s</td><td align="right">216GB</td><td align="right">975ms</td><td align="right">11.7MB</td><td align="right"></td></tr>
<tr><td><code>Log</code> with a <code>PathFilter</code> over full history</td><td align="right">44.9s</td><td align="right">54.6GB</td><td align="right">4.48s</td><td align="right">1.83GB</td><td align="right">528ms</td></tr>
<tr><td>The same with <code>FileName</code></td><td align="right"></td><td align="right"></td><td align="right">736ms</td><td align="right">11.9MB</td><td align="right"></td></tr>
<tr><td><code>Log</code> from <code>HEAD</code> over every commit</td><td align="right">937ms</td><td align="right">474MB</td><td align="right">439ms</td><td align="right">87.1MB</td><td align="right"></td></tr>
<tr><td>Commits from <code>HEAD</code> of the last 90 days</td><td align="right">859ms</td><td align="right">473MB</td><td align="right">14ms</td><td align="right">6.48MB</td><td align="right">10ms</td></tr>
<tr><td>List the file names at <code>HEAD</code></td><td align="right">1.45s</td><td align="right">601MB</td><td align="right">35ms</td><td align="right">23MB</td><td align="right">35ms</td></tr>
<tr><td><code>Grep</code> for files by name</td><td align="right">1.6s</td><td align="right">605MB</td><td align="right">161ms</td><td align="right">20MB</td><td align="right"></td></tr>
<tr><td>Find a tag by pattern and peel it</td><td align="right">118ms</td><td align="right">129MB</td><td align="right">11ms</td><td align="right">1.06MB</td><td align="right"></td></tr>
<tr><td>Resolve every tag and pick the highest version</td><td align="right">3.29s</td><td align="right">7.3GB</td><td align="right">295ms</td><td align="right">121MB</td><td align="right"></td></tr>
<tr><td>Recent commits, on any branch, that touch the package's directory</td><td align="right">145s</td><td align="right">216GB</td><td align="right">430ms</td><td align="right">392MB</td><td align="right">57ms</td></tr>
<tr><td>The same with <code>FileName</code></td><td align="right"></td><td align="right"></td><td align="right">94ms</td><td align="right">2.37MB</td><td align="right"></td></tr>
<tr><td><code>FindEntry</code> and a blob read in each of 3,000 commits</td><td align="right">467ms</td><td align="right">419MB</td><td align="right">108ms</td><td align="right">25.4MB</td><td align="right"></td></tr>
<tr><td>Every commit reachable from any reference</td><td align="right">971ms</td><td align="right">528MB</td><td align="right">505ms</td><td align="right">94.8MB</td><td align="right"></td></tr>
<tr><td>Read every blob at <code>HEAD</code></td><td align="right">17.5s</td><td align="right">12.4GB</td><td align="right">5.61s</td><td align="right">57.5MB</td><td align="right"></td></tr>
<tr><td>Warm: resolve <code>HEAD</code> and read the commit</td><td align="right">0.077ms</td><td align="right">115KB</td><td align="right">0.031ms</td><td align="right">5.43KB</td><td align="right"></td></tr>
<tr><td>Warm: read 10 files from a loaded tree</td><td align="right">0.054ms</td><td align="right">1.25MB</td><td align="right">0.062ms</td><td align="right">297KB</td><td align="right"></td></tr>
</table>

### Improvement over go-git

Each cell is go-git's time or allocation divided by go-git2's.

<table>
<tr><th rowspan="2">Workload</th><th colspan="2">small</th><th colspan="2">medium</th><th colspan="2">large</th></tr>
<tr><th>Time</th><th>Allocated</th><th>Time</th><th>Allocated</th><th>Time</th><th>Allocated</th></tr>
<tr><td>Open, resolve <code>HEAD</code> and read the commit</td><td align="right">7.6×</td><td align="right">271×</td><td align="right">16×</td><td align="right">2,069×</td><td align="right">20×</td><td align="right">3,893×</td></tr>
<tr><td>Find the commits that changed the package's version, from <code>HEAD</code></td><td align="right">27×</td><td align="right">1,677×</td><td align="right">104×</td><td align="right">12,802×</td><td align="right">160×</td><td align="right">19,350×</td></tr>
<tr><td>The same over all references, in committer-time order</td><td align="right">27×</td><td align="right">1,296×</td><td align="right">73×</td><td align="right">8,656×</td><td align="right">146×</td><td align="right">18,459×</td></tr>
<tr><td><code>Log</code> with a <code>PathFilter</code> over full history</td><td align="right">14×</td><td align="right">24×</td><td align="right">20×</td><td align="right">30×</td><td align="right">10×</td><td align="right">30×</td></tr>
<tr><td><code>Log</code> from <code>HEAD</code> over every commit</td><td align="right">1.7×</td><td align="right">3.7×</td><td align="right">1.8×</td><td align="right">4.4×</td><td align="right">2.1×</td><td align="right">5.4×</td></tr>
<tr><td>Commits from <code>HEAD</code> of the last 90 days</td><td align="right">69×</td><td align="right">67×</td><td align="right">96×</td><td align="right">221×</td><td align="right">61×</td><td align="right">73×</td></tr>
<tr><td>List the file names at <code>HEAD</code></td><td align="right">37×</td><td align="right">81×</td><td align="right">32×</td><td align="right">55×</td><td align="right">41×</td><td align="right">26×</td></tr>
<tr><td><code>Grep</code> for files by name</td><td align="right">14×</td><td align="right">104×</td><td align="right">17×</td><td align="right">66×</td><td align="right">10×</td><td align="right">30×</td></tr>
<tr><td>Find a tag by pattern and peel it</td><td align="right">6×</td><td align="right">112×</td><td align="right">16×</td><td align="right">540×</td><td align="right">11×</td><td align="right">122×</td></tr>
<tr><td>Resolve every tag and pick the highest version</td><td align="right">2.5×</td><td align="right">46×</td><td align="right">4×</td><td align="right">125×</td><td align="right">11×</td><td align="right">61×</td></tr>
<tr><td>Recent commits, on any branch, that touch the package's directory</td><td align="right">418×</td><td align="right">1,864×</td><td align="right">1,018×</td><td align="right">1,685×</td><td align="right">338×</td><td align="right">551×</td></tr>
<tr><td><code>FindEntry</code> and a blob read in each of 3,000 commits</td><td align="right">2.3×</td><td align="right">4.1×</td><td align="right">3.1×</td><td align="right">9×</td><td align="right">4.3×</td><td align="right">16×</td></tr>
<tr><td>Every commit reachable from any reference</td><td align="right">1.8×</td><td align="right">3.9×</td><td align="right">1.3×</td><td align="right">3.7×</td><td align="right">1.9×</td><td align="right">5.6×</td></tr>
<tr><td>Read every blob at <code>HEAD</code></td><td align="right">2.9×</td><td align="right">51×</td><td align="right">3×</td><td align="right">51×</td><td align="right">3.1×</td><td align="right">216×</td></tr>
<tr><td>Warm: resolve <code>HEAD</code> and read the commit</td><td align="right">1.1×</td><td align="right">6.5×</td><td align="right">1×</td><td align="right">4.8×</td><td align="right">2.5×</td><td align="right">21×</td></tr>
<tr><td>Warm: read 10 files from a loaded tree</td><td align="right">4.2×</td><td align="right">4.5×</td><td align="right">3.3×</td><td align="right">3.9×</td><td align="right">0.86×</td><td align="right">4.2×</td></tr>
</table>

- The parity tests in `bench/` check that both libraries return the same
  results, or, where go-git is wrong, that go-git2 matches `git`. On the
  large repository, go-git's walk of every reference misses 2,445 of the
  70,808 reachable commits, and on the medium one 13,682 of 52,906.
- The query for recent commits under the package's directory runs in
  committer-time order, git log's default, in which go-git2 stops at the
  cutoff. There go-git returns the same commits as git, 18 on the large
  repository and 67 on the medium one, and takes 145s on the large one. In
  its default order, go-git takes 69s there and reports 37 commits.
- Allocations for go-git2 exclude the sandbox's linear memory, which is
  mapped outside the Go heap and released by `Close`. On the large
  repository, it is about 40MiB for lookups and 125MiB after walking every
  commit.
- The git CLI column has no entry for reading every blob at `HEAD`.
  `git cat-file --batch` does it in 2.29s on the large repository.
- go-git keeps decoded blobs of any size in its object cache, while
  go-git2 caches blobs up to 64KB. Re-reading large files that are already
  loaded is therefore slower: two of the ten files read on the large
  repository are over 64KB.
- Opening a repository from a go-billy `memfs` gives results within a few
  percent of these.

## Running

The benchmarks live in a separate module so that go-git2 does not depend on
go-git. `cmd/benchrepos` builds the repositories into `../.cache`, which
takes about 1.3GB. Then, from this directory:

```sh
go run ./cmd/benchrepos all
go test -run Parity .
{
  go test -run '^$' -bench . -skip Warm -benchtime 3x .
  go test -run '^$' -bench Warm -benchtime 2s .
} | go run ./cmd/table
```

Cold benchmarks run a fixed number of times. The warm ones take
microseconds, so they run for a fixed time. `cmd/table` turns the output into
the tables above, taking the median of repeated runs.

Set `GIT2_BENCH_REPO` to `small`, `medium`, `large` or a comma-separated
list to run on some of the repositories, `GIT2_BENCH_DIR` to move the cache,
and `GIT2_BENCH_VERIFY=1` to measure go-git2 with hash verification on.
