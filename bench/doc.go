// Package bench compares git2 against go-git on read-only workloads
// modeled on real git consumers. It is a separate module so that the
// library does not depend on go-git.
//
// The workloads run on three repositories of different sizes, each pinned
// to a point in its upstream history. repos.go defines them, and
// cmd/benchrepos builds them into ../.cache, which takes about 1.3 GB:
//
//	go run ./cmd/benchrepos all
//
// $GIT2_BENCH_REPO selects repositories by size or name, as a
// comma-separated list such as small,medium. It selects all of them by
// default. $GIT2_BENCH_DIR moves the cache. Benchmarks and parity tests skip
// repositories that are not built.
//
// cmd/table turns benchmark output into the tables in README.md. Cold
// benchmarks run a fixed number of times, and the warm ones, which take
// microseconds, for a fixed time:
//
//	{
//		go test -run '^$' -bench . -skip Warm -benchtime 3x .
//		go test -run '^$' -bench Warm -benchtime 2s .
//	} | go run ./cmd/table
package bench
