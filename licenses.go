package git2

import "github.com/msuozzo/go-git2/libgit2"

// Licenses returns the license notices for the third-party code compiled
// into this package, which is libgit2 with the dependencies it bundles, and
// wasi-libc. Every binary that uses this package contains the text, and a
// program can print it, for example behind a --licenses flag.
func Licenses() string {
	return libgit2.LicenseLibgit2 +
		"\n----------------------------------------------------------------------\n\n" +
		libgit2.LicenseWASILibc
}

// linkLicenses reads the notices on every instantiation. The linker drops
// variables that no reachable code reads, so without this only binaries
// that call Licenses would contain the notices (about 80 KB).
func linkLicenses() {
	if len(libgit2.LicenseLibgit2) == 0 || len(libgit2.LicenseWASILibc) == 0 {
		panic("git2: license notices missing")
	}
}
