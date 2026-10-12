package git2_test

import (
	"fmt"
	"log"
	"regexp"

	"github.com/msuozzo/go-git2"
)

func Example() {
	r, err := git2.PlainOpen("/path/to/repo.git")
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	head, err := r.Head()
	if err != nil {
		log.Fatal(err)
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		log.Fatal(err)
	}
	f, err := c.File("setup.py")
	if err != nil {
		log.Fatal(err)
	}
	src, err := f.Contents()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(c.Committer.When, len(src))
}

// Commits that changed a manifest, newest first. FileName avoids diffing
// whole trees, and PathFilter accepts any predicate.
func ExampleRepository_Log() {
	r, err := git2.PlainOpen("/path/to/repo.git")
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	manifest := "packages/foo/package.json"
	commits, err := r.Log(&git2.LogOptions{
		All:      true,
		Order:    git2.LogOrderCommitterTime,
		FileName: &manifest,
	})
	if err != nil {
		log.Fatal(err)
	}
	var buf []byte
	err = commits.ForEach(func(c *git2.Commit) error {
		f, err := c.File(manifest)
		if err != nil {
			return nil // deleted in this commit
		}
		// Reusing buf avoids allocating per file.
		buf, err = f.Bytes(buf[:0])
		fmt.Println(c.Hash, len(buf))
		return err
	})
	if err != nil {
		log.Fatal(err)
	}
}

func ExampleRepository_Grep() {
	r, err := git2.PlainOpen("/path/to/repo.git")
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	results, err := r.Grep(&git2.GrepOptions{
		PathSpecs: []*regexp.Regexp{regexp.MustCompile(`(^|/)package\.json$`)},
		Patterns:  []*regexp.Regexp{regexp.MustCompile(`"name":\s*"left-pad"`)},
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, res := range results {
		fmt.Println(res.FileName, res.LineNumber)
	}
}
