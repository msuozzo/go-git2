//go:build (!unix && !windows) || git2_heapmem

package git2

// Without a reservation, or with the git2_heapmem tag for comparison,
// linear memory lives on the Go heap and grows by doubling. See newMemory.

func reserve(size int) ([]byte, bool) { return nil, false }

func commit([]byte) bool { return true }

func release([]byte) {}
