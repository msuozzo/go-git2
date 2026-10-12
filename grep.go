package git2

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
)

// GrepOptions select the commit, files and lines that Grep searches.
type GrepOptions struct {
	// Patterns select lines matching any of them.
	Patterns []*regexp.Regexp
	// InvertMatch selects non-matching lines.
	InvertMatch bool
	// CommitHash is the commit to search. If both it and ReferenceName are
	// unset, HEAD is searched.
	CommitHash Hash
	// ReferenceName is the reference to search.
	ReferenceName ReferenceName
	// PathSpecs restrict the search to files whose path matches one.
	PathSpecs []*regexp.Regexp
}

// ErrHashOrReference is returned when both CommitHash and ReferenceName
// are set.
var ErrHashOrReference = errors.New("ambiguous options, only one of CommitHash or ReferenceName can be passed")

// GrepResult is one matching line.
type GrepResult struct {
	FileName   string
	LineNumber int
	Content    string
	TreeName   string
}

func (gr GrepResult) String() string {
	return gr.TreeName + ":" + gr.FileName + ":" + strconv.Itoa(gr.LineNumber) + ":" + gr.Content
}

// Grep searches the files of a commit line by line and returns the
// selected lines. Only files selected by PathSpecs are read.
func (r *Repository) Grep(o *GrepOptions) ([]GrepResult, error) {
	if !o.CommitHash.IsZero() && o.ReferenceName != "" {
		return nil, ErrHashOrReference
	}
	var commit Hash
	var treeName string
	switch {
	case o.ReferenceName != "":
		ref, err := r.Reference(o.ReferenceName, true)
		if err != nil {
			return nil, err
		}
		commit, treeName = ref.Hash(), o.ReferenceName.String()
	case !o.CommitHash.IsZero():
		commit, treeName = o.CommitHash, o.CommitHash.String()
	default:
		head, err := r.Head()
		if err != nil {
			return nil, err
		}
		commit, treeName = head.Hash(), head.Hash().String()
	}
	c, err := r.CommitObject(commit)
	if err != nil {
		return nil, err
	}
	ents, err := r.walk(c.TreeHash, 0)
	if err != nil {
		return nil, err
	}

	var results []GrepResult
	var buf []byte
	for _, e := range ents {
		if !pathSelected(o.PathSpecs, e.path) {
			continue
		}
		if buf, err = r.ReadBlob(e.hash, buf[:0]); err != nil {
			return nil, err
		}
		results = grepLines(results, buf, e.path, treeName, o)
	}
	return results, nil
}

func pathSelected(specs []*regexp.Regexp, name string) bool {
	if len(specs) == 0 {
		return true
	}
	for _, s := range specs {
		if s != nil && s.MatchString(name) {
			return true
		}
	}
	return false
}

// grepLines appends results for the lines of content. Like go-git, it splits
// on "\n", so a trailing newline yields a final empty line.
func grepLines(results []GrepResult, content []byte, name, treeName string, o *GrepOptions) []GrepResult {
	for n := 1; ; n++ {
		line := content
		nl := bytes.IndexByte(content, '\n')
		if nl >= 0 {
			line = content[:nl]
		}
		add := false
		for _, p := range o.Patterns {
			if p != nil && p.Match(line) {
				if !o.InvertMatch {
					add = true
					break
				}
			} else if o.InvertMatch {
				add = true
				break
			}
		}
		if add {
			results = append(results, GrepResult{
				FileName: name, LineNumber: n, Content: string(line), TreeName: treeName,
			})
		}
		if nl < 0 {
			return results
		}
		content = content[nl+1:]
	}
}
