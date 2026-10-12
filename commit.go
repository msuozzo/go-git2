package git2

import (
	"fmt"
	"strings"
)

// Commit is a parsed commit object.
type Commit struct {
	Hash         Hash
	Author       Signature
	Committer    Signature
	PGPSignature string
	Message      string
	TreeHash     Hash
	ParentHashes []Hash
	Encoding     string
	MergeTag     string

	r       *Repository
	parents [2]Hash // backing store for up to two parents
}

// decodeCommit parses a raw commit object. Strings alias raw.
func decodeCommit(r *Repository, h Hash, raw string) (*Commit, error) {
	c := &Commit{Hash: h, r: r}
	if err := c.decodeRaw(raw); err != nil {
		return nil, err
	}
	return c, nil
}

// decodeRaw parses a raw commit object. Strings alias raw.
func (c *Commit) decodeRaw(raw string) error {
	hdr := raw
	if i := strings.Index(raw, "\n\n"); i >= 0 {
		hdr, c.Message = raw[:i+1], raw[i+2:]
	}
	return c.decode(hdr)
}

// Positions of a commit's canonical header lines, in order.
const (
	atTree = iota
	inParents
	atAuthor
	atCommitter
	inHeaders
)

// decode parses commit header lines. Git has no single rule for malformed
// objects, so this decoder and those for tags, trees and signatures give the
// values go-git does for them.
//
// The first line must hold the tree, and the parents come right after it.
// The author is read only from the line after the parents, and the
// committer only from the line after that. Other tree, parent, author and
// committer lines are ignored. Strings alias hdr, except multi-line values,
// which are copied.
func (c *Commit) decode(hdr string) error {
	c.ParentHashes = c.parents[:0]
	c.Encoding = "UTF-8"
	sawEncoding, sawMergetag := false, false
	pos := atTree
	for len(hdr) > 0 {
		line, rest := cutLine(hdr)
		key, val, _ := strings.Cut(line, " ")
		// The value and whatever follows it, for multi-line values.
		from := hdr[len(line)-len(val):]
		hdr = rest
		switch pos {
		case atTree:
			if len(line) == 0 {
				return fmt.Errorf("%w: missing tree header", ErrMalformedCommit)
			}
			if key != "tree" {
				return fmt.Errorf("%w: tree header must be first", ErrMalformedCommit)
			}
			h, ok := parseID(val)
			if !ok {
				return fmt.Errorf("%w: bad tree hash", ErrMalformedCommit)
			}
			c.TreeHash, pos = h, inParents
			continue
		case inParents:
			if key == "parent" {
				h, ok := parseID(val)
				if !ok {
					return fmt.Errorf("%w: bad parent hash", ErrMalformedCommit)
				}
				c.ParentHashes = append(c.ParentHashes, h)
				continue
			}
			pos = atAuthor
			fallthrough
		case atAuthor:
			pos = atCommitter
			if key == "author" {
				c.Author.decode(val)
				continue
			}
			fallthrough
		case atCommitter:
			pos = inHeaders
			if key == "committer" {
				c.Committer.decode(val)
				continue
			}
		}
		switch key {
		case "tree", "parent", "author", "committer":
			// Out of place: ignored, as in git.
		case "encoding":
			if !sawEncoding {
				c.Encoding, sawEncoding = val, true
			}
		case "gpgsig":
			var sig string
			sig, rest = continued(from)
			c.PGPSignature += sig
		case "mergetag":
			if !sawMergetag {
				c.MergeTag, rest = continued(from)
				sawMergetag = true
			} else {
				rest = skipContinued(rest)
			}
		default: // gpgsig-sha256 and other headers are not kept
			rest = skipContinued(rest)
		}
		hdr = rest
	}
	if pos == atTree {
		return fmt.Errorf("%w: missing tree header", ErrMalformedCommit)
	}
	return nil
}

// cutLine splits off the first line, without its newline.
func cutLine(s string) (line, rest string) {
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		return s[:nl], s[nl+1:]
	}
	return s, ""
}

// continued returns the header value that starts s and its continuation
// lines, each ending in a newline, as go-git formats them, and the input
// after them. Continuation lines start with a space, which is dropped. A
// value on one line aliases s, and a longer one is copied.
func continued(s string) (val, rest string) {
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return s + "\n", ""
	}
	// end is where the continuation lines end, and k counts them.
	end, k := nl+1, 0
	for end < len(s) && s[end] == ' ' {
		k++
		if i := strings.IndexByte(s[end:], '\n'); i >= 0 {
			end += i + 1
		} else {
			end = len(s)
		}
	}
	if k == 0 {
		return s[:end], s[end:]
	}
	var b strings.Builder
	b.Grow(end - k)
	b.WriteString(s[:nl+1])
	for line := s[nl+1 : end]; len(line) > 0; {
		next := len(line)
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			next = i + 1
		}
		b.WriteString(line[1:next])
		line = line[next:]
	}
	return b.String(), s[end:]
}

func skipContinued(rest string) string {
	for len(rest) > 0 && rest[0] == ' ' {
		_, rest = cutLine(rest)
	}
	return rest
}

// ID returns the commit's hash.
func (c *Commit) ID() Hash { return c.Hash }

// Type returns CommitObject.
func (c *Commit) Type() ObjectType { return CommitObject }

// NumParents returns the number of parents.
func (c *Commit) NumParents() int { return len(c.ParentHashes) }

// Tree returns the commit's root tree.
func (c *Commit) Tree() (*Tree, error) { return c.r.TreeObject(c.TreeHash) }

// Parent returns the i-th parent.
func (c *Commit) Parent(i int) (*Commit, error) {
	if i < 0 || i >= len(c.ParentHashes) {
		return nil, ErrObjectNotFound
	}
	return c.r.CommitObject(c.ParentHashes[i])
}

// Parents iterates over the commit's parents.
func (c *Commit) Parents() *CommitIter {
	return &CommitIter{r: c.r, list: true, ids: c.ParentHashes}
}

// File returns the file at path in the commit's tree, or ErrFileNotFound.
func (c *Commit) File(path string) (*File, error) {
	return c.r.file(c.TreeHash, path)
}

// Files iterates over every file in the commit's tree.
func (c *Commit) Files() (*FileIter, error) {
	return &FileIter{r: c.r, tree: c.TreeHash}, nil
}

func (c *Commit) String() string {
	return "commit " + c.Hash.String() + "\nAuthor: " + c.Author.String() +
		"\nDate:   " + c.Author.When.Format("Mon Jan 02 15:04:05 2006 -0700") + "\n\n" +
		indent(c.Message) + "\n"
}

func indent(t string) string {
	lines := strings.Split(t, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "    " + l
		}
	}
	return strings.Join(lines, "\n")
}
