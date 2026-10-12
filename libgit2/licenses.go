package libgit2

import _ "embed"

// LicenseLibgit2 is the license of libgit2 and of the dependencies it
// bundles, whose compiled form this package embeds. It is also the license
// of this module.
//
//go:embed LICENSE
var LicenseLibgit2 string

// LicenseWASILibc holds the licenses of wasi-libc, including musl and
// cloudlibc, whose compiled form this package embeds.
//
//go:embed LICENSE.wasi-libc
var LicenseWASILibc string
