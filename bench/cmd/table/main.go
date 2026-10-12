// Command table formats benchmark output as the tables in README.md. Run it
// from bench/:
//
//	{
//		go test -run '^$' -bench . -skip Warm -benchtime 3x .
//		go test -run '^$' -bench Warm -benchtime 2s .
//	} | go run ./cmd/table
//
// It prints a table for each repository, then go-git2's time and allocation
// against go-git's for every repository side by side. The per-repository
// tables are HTML, to group each library's time and allocation under one
// heading. The git CLI column, where a benchmark has one, is git doing the
// same work. Repeated runs are summarized by their median.
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var line = regexp.MustCompile(`^Benchmark([^/\s]+)/([^/\s]+)/(go-git|git2(?:-FileName)?|git)-\d+\s+\d+\s+([\d.]+) ns/op(?:\s+(\d+) B/op\s+\d+ allocs/op)?`)

// workloads describes each benchmark, in table order.
var workloads = []struct{ name, desc string }{
	{"OpenHeadCommit", "Open, resolve `HEAD` and read the commit"},
	{"VersionHistory", "Find the commits that changed the package's version, from `HEAD`"},
	{"VersionHistoryAll", "The same over all references, in committer-time order"},
	{"LogPathFilter", "`Log` with a `PathFilter` over full history"},
	{"LogHead", "`Log` from `HEAD` over every commit"},
	{"RecentLog", "Commits from `HEAD` of the last 90 days"},
	{"TreeFiles", "List the file names at `HEAD`"},
	{"Grep", "`Grep` for files by name"},
	{"FindTagMatch", "Find a tag by pattern and peel it"},
	{"TagSemver", "Resolve every tag and pick the highest version"},
	{"RecentPathLog", "Recent commits, on any branch, that touch the package's directory"},
	{"EntryWalk", "`FindEntry` and a blob read in each of 3,000 commits"},
	{"CommitGraph", "Every commit reachable from any reference"},
	{"BlobsAtHead", "Read every blob at `HEAD`"},
	{"HeadCommitWarm", "Warm: resolve `HEAD` and read the commit"},
	{"FileContentsWarm", "Warm: read 10 files from a loaded tree"},
}

var sizes = []string{"small", "medium", "large"}

type key struct{ name, repo, impl string }

// result is a benchmark's time in nanoseconds and bytes allocated per op.
type result struct{ ns, b float64 }

func main() {
	runs := map[key][]result{}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		m := line.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil {
			continue
		}
		ns, _ := strconv.ParseFloat(m[4], 64)
		b, _ := strconv.ParseFloat(m[5], 64) // 0 without -benchmem
		k := key{m[1], m[2], m[3]}
		runs[k] = append(runs[k], result{ns, b})
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "table:", err)
		os.Exit(1)
	}
	res := map[key]result{}
	for k, rs := range runs {
		res[k] = result{
			median(rs, func(r result) float64 { return r.ns }),
			median(rs, func(r result) float64 { return r.b }),
		}
	}
	var repos []string
	for _, s := range sizes {
		for k := range res {
			if k.repo == s {
				repos = append(repos, s)
				break
			}
		}
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	row := func(label string, cells ...string) {
		fmt.Fprintf(w, "<tr><td>%s</td>", cell(label))
		for _, c := range cells {
			fmt.Fprintf(w, `<td align="right">%s</td>`, c)
		}
		fmt.Fprintln(w, "</tr>")
	}
	for _, repo := range repos {
		fmt.Fprintf(w, "### %s\n\n", repo)
		fmt.Fprintln(w, "<table>")
		fmt.Fprintln(w, `<tr><th rowspan="2">Workload</th><th colspan="2">go-git</th><th colspan="2">go-git2</th><th>git CLI</th></tr>`)
		fmt.Fprintln(w, "<tr><th>Time</th><th>Allocated</th><th>Time</th><th>Allocated</th><th>Time</th></tr>")
		for _, wl := range workloads {
			g, ok1 := res[key{wl.name, repo, "go-git"}]
			x, ok2 := res[key{wl.name, repo, "git2"}]
			if !ok1 || !ok2 {
				continue
			}
			cli := ""
			if c, ok := res[key{wl.name, repo, "git"}]; ok {
				cli = dur(c.ns)
			}
			row(wl.desc, dur(g.ns), size(g.b), dur(x.ns), size(x.b), cli)
			if fn, ok := res[key{wl.name, repo, "git2-FileName"}]; ok {
				row("The same with `FileName`", "", "", dur(fn.ns), size(fn.b), "")
			}
		}
		fmt.Fprint(w, "</table>\n\n")
	}

	fmt.Fprint(w, "### Improvement over go-git\n\n")
	fmt.Fprint(w, "Each cell is go-git's time or allocation divided by go-git2's.\n\n")
	fmt.Fprintln(w, "<table>")
	fmt.Fprint(w, `<tr><th rowspan="2">Workload</th>`)
	for _, repo := range repos {
		fmt.Fprintf(w, `<th colspan="2">%s</th>`, repo)
	}
	fmt.Fprintln(w, "</tr>")
	fmt.Fprintln(w, "<tr>"+strings.Repeat("<th>Time</th><th>Allocated</th>", len(repos))+"</tr>")
	for _, wl := range workloads {
		var cells []string
		found := false
		for _, repo := range repos {
			g, ok1 := res[key{wl.name, repo, "go-git"}]
			x, ok2 := res[key{wl.name, repo, "git2"}]
			if ok1 && ok2 {
				cells = append(cells, ratio(g.ns, x.ns), ratio(g.b, x.b))
				found = true
			} else {
				cells = append(cells, "", "")
			}
		}
		if found {
			row(wl.desc, cells...)
		}
	}
	fmt.Fprintln(w, "</table>")
}

// median returns the median of f over rs, the mean of the middle two for an
// even count.
func median(rs []result, f func(result) float64) float64 {
	v := make([]float64, len(rs))
	for i, r := range rs {
		v[i] = f(r)
	}
	slices.Sort(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

// dur formats seconds to three significant figures, then whole
// milliseconds, and below a millisecond, milliseconds to three places.
func dur(ns float64) string {
	if ns >= 1e9 {
		return strconv.FormatFloat(ns/1e9, 'g', 3, 64) + "s"
	}
	ms := ns / 1e6
	if ms >= 1 {
		return strconv.FormatFloat(ms, 'f', 0, 64) + "ms"
	}
	return strconv.FormatFloat(ms, 'f', 3, 64) + "ms"
}

func size(b float64) string {
	for _, u := range []struct {
		unit  string
		scale float64
	}{{"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}} {
		if b >= u.scale {
			return strconv.FormatFloat(b/u.scale, 'g', 3, 64) + u.unit
		}
	}
	return strconv.FormatFloat(b, 'f', 0, 64) + "B"
}

func ratio(a, b float64) string {
	r := a / b
	if r >= 10 {
		return thousands(strconv.FormatFloat(r, 'f', 0, 64)) + "×"
	}
	return strconv.FormatFloat(r, 'g', 2, 64) + "×"
}

// thousands puts commas between groups of three digits.
func thousands(s string) string {
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

var (
	codeSpan = regexp.MustCompile("`([^`]+)`")
	escaper  = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
)

// cell formats Markdown code spans for an HTML cell, where they don't render.
func cell(text string) string {
	return codeSpan.ReplaceAllString(escaper.Replace(text), "<code>$1</code>")
}
