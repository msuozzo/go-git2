package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Repo is a benchmark repository: an upstream repository pinned to a point
// in its history, and the parameters its workloads use.
//
// cmd/benchrepos builds each repository from upstream. It bare-clones it,
// moves every branch back to its newest first-parent commit at or before
// Cutoff, drops branches with none and tags made after Cutoff, checks that
// Branch ends at Head, and repacks. The same git version gives the same
// pack.
//
// Only Branch is checked against Head. The rewind cannot bring back
// branches deleted upstream after Cutoff, and it keeps branches and
// lightweight tags created later on older commits, so the workloads over
// every reference are only approximately repeatable.
type Repo struct {
	Size   string // small, medium or large: the benchmark level and GIT2_BENCH_REPO value
	Name   string // the directory in the cache, without .git
	URL    string
	Cutoff time.Time
	Branch string // the default branch
	Head   string // the commit Branch must end at

	// ApproxSize is about the size of the built repository in bytes.
	// Building it needs twice that free.
	ApproxSize int64

	// Dir holds the code of the package whose history the workloads
	// follow. RecentPathLog filters on it, and the parity tests compare its
	// subtrees.
	Dir string

	// VersionFile holds the package's version, which the first group of
	// VersionRE extracts.
	VersionFile string
	VersionRE   *regexp.Regexp

	// Release tags have the form TagPrefix followed by X.Y.Z. TagSemver
	// reads them all, and FindTagMatch looks for the one for TagVersion.
	TagPrefix  string
	TagVersion string

	// Manifests are the files FileContents reads at HEAD.
	Manifests []string

	// Grep searches the files at HEAD whose paths match GrepPath for
	// GrepPattern.
	GrepPath, GrepPattern *regexp.Regexp

	// RecentWindow is how far back from HEAD's commit time RecentPathLog
	// looks.
	RecentWindow time.Duration
}

var pythonVersion = regexp.MustCompile(`__version__\s*=\s*"([^"]+)"`)

// Repos are the benchmark repositories, smallest first.
var Repos = []*Repo{
	{
		Size:       "small",
		Name:       "celery",
		URL:        "https://github.com/celery/celery",
		Cutoff:     time.Date(2026, 3, 26, 12, 9, 52, 0, time.UTC), // the v5.6.3 tag
		Branch:     "main",
		Head:       "c38600d429eb0d457ce57ae4ec6b52fe9e40a28d",
		ApproxSize: 45 << 20,

		Dir:         "celery",
		VersionFile: "celery/__init__.py",
		VersionRE:   regexp.MustCompile(`__version__\s*=\s*['"]([^'"]+)['"]`),
		TagPrefix:   "v",
		TagVersion:  "5.6.3",
		Manifests: []string{
			"pyproject.toml",
			"setup.py",
			"setup.cfg",
			"celery/__init__.py",
			"README.rst",
			"requirements/default.txt",
			"requirements/test.txt",
			"tox.ini",
			"MANIFEST.in",
			"CONTRIBUTORS.txt",
		},
		GrepPath:     regexp.MustCompile(`(^|/)setup\.py$`),
		GrepPattern:  regexp.MustCompile(`^NAME\s*=\s*'celery'`),
		RecentWindow: 90 * 24 * time.Hour,
	},
	{
		Size:   "medium",
		Name:   "django",
		URL:    "https://github.com/django/django",
		Cutoff: time.Date(2026, 10, 6, 11, 39, 41, 0, time.UTC), // the 6.1.2 tag
		Branch: "main",
		Head:   "62ef9d40cf4ea75f16518436b44b8ab2515340df",

		ApproxSize: 180 << 20,

		Dir:         "django/db",
		VersionFile: "django/__init__.py",
		VersionRE:   regexp.MustCompile(`VERSION\s*=\s*(?:VersionTuple)?\(([^)]*)\)`),
		TagPrefix:   "",
		TagVersion:  "6.1.2",
		Manifests: []string{
			"pyproject.toml",
			"django/__init__.py",
			"README.rst",
			"AUTHORS",
			"LICENSE",
			"MANIFEST.in",
			"tox.ini",
			"package.json",
			"INSTALL",
			"CONTRIBUTING.rst",
		},
		GrepPath:     regexp.MustCompile(`(^|/)pyproject\.toml$`),
		GrepPattern:  regexp.MustCompile(`^name\s*=\s*"Django"`),
		RecentWindow: 90 * 24 * time.Hour,
	},
	{
		Size:   "large",
		Name:   "google-cloud-python",
		URL:    "https://github.com/googleapis/google-cloud-python",
		Cutoff: time.Date(2026, 10, 10, 10, 41, 19, 0, time.UTC),
		Branch: "main",
		Head:   "05b37756b0c4cb43ae978c928e90b1ff40a07ccd",

		ApproxSize: 1100 << 20,

		Dir:         "packages/google-cloud-language",
		VersionFile: "packages/google-cloud-language/google/cloud/language/gapic_version.py",
		VersionRE:   pythonVersion,
		TagPrefix:   "google-cloud-language-v",
		TagVersion:  "2.22.0",
		Manifests: []string{
			"packages/google-cloud-language/setup.py",
			"packages/google-cloud-language/google/cloud/language/gapic_version.py",
			"packages/google-cloud-language/README.rst",
			"packages/google-cloud-language/noxfile.py",
			"packages/google-cloud-storage/setup.py",
			"packages/google-cloud-spanner/setup.py",
			"packages/google-cloud-bigquery-storage/setup.py",
			"README.rst",
			"release-please-individual-config.json",
			"librarian.yaml",
		},
		GrepPath:     regexp.MustCompile(`.*/setup\.py$`),
		GrepPattern:  regexp.MustCompile(`name\s*=\s*["']google-cloud-language["']`),
		RecentWindow: 90 * 24 * time.Hour,
	},
}

// Path is where the repository is built: the directory named by
// $GIT2_BENCH_DIR, or ../.cache by default.
func (r *Repo) Path() string {
	dir := os.Getenv("GIT2_BENCH_DIR")
	if dir == "" {
		dir = filepath.Join("..", ".cache")
	}
	return filepath.Join(dir, r.Name+".git")
}

// Selected returns the repositories that $GIT2_BENCH_REPO names, as a
// comma-separated list of sizes or names. Unset or "all" selects every
// repository.
func Selected() ([]*Repo, error) {
	return Select(os.Getenv("GIT2_BENCH_REPO"))
}

// Select returns the repositories that spec names, as Selected does.
func Select(spec string) ([]*Repo, error) {
	if spec == "" || spec == "all" {
		return Repos, nil
	}
	var out []*Repo
	for _, s := range strings.Split(spec, ",") {
		i := slices.IndexFunc(Repos, func(r *Repo) bool { return r.Size == s || r.Name == s })
		if i < 0 {
			return nil, fmt.Errorf("unknown benchmark repository %q", s)
		}
		out = append(out, Repos[i])
	}
	return out, nil
}
