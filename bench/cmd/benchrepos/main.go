// Command benchrepos builds the benchmark repositories that bench.Repos
// defines, and prints a table of their statistics. Run it from bench/, so
// that the repositories land in ../.cache:
//
//	go run ./cmd/benchrepos [-stats] [small|medium|large|all|name,...]
//
// A repository that is already built at its pinned commit is kept.
// Building one needs about twice its size in free disk space, for the
// clone and its repack. git runs without the user's or the system's
// configuration, which could change the pack it writes.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/msuozzo/go-git2/bench"
)

func main() {
	stats := flag.Bool("stats", false, "print statistics only and build nothing")
	flag.Parse()
	if mod, err := os.ReadFile("go.mod"); err != nil || !strings.HasPrefix(string(mod), "module github.com/msuozzo/go-git2/bench\n") {
		fatal(errors.New("run benchrepos from the bench directory"))
	}
	repos, err := bench.Select(strings.Join(flag.Args(), ","))
	if err != nil {
		fatal(err)
	}
	if !*stats {
		for _, r := range repos {
			if err := build(r); err != nil {
				fatal(fmt.Errorf("%s: %w", r.Name, err))
			}
		}
	}
	printStats(repos)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "benchrepos:", err)
	os.Exit(1)
}

// command returns a git command run in dir, without the user's or the
// system's configuration.
func command(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	return cmd
}

// git runs git in dir and returns its trimmed output.
func git(dir string, args ...string) (string, error) {
	cmd := command(dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(stderr.Bytes()))
	}
	return strings.TrimSpace(string(out)), nil
}

func build(r *bench.Repo) (err error) {
	dst, err := filepath.Abs(r.Path())
	if err != nil {
		return err
	}
	if head, err := git(dst, "rev-parse", "refs/heads/"+r.Branch); err == nil {
		if head == r.Head {
			fmt.Printf("%s: built at %s\n", r.Name, head)
			return nil
		}
		return fmt.Errorf("%s is at %s, not the pinned %s; remove it to rebuild", dst, head, r.Head)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if free, need := freeBytes(filepath.Dir(dst)), 2*r.ApproxSize; free < need {
		return fmt.Errorf("building it needs about %d MiB free in %s, and %d MiB are", need>>20, filepath.Dir(dst), free>>20)
	}
	tmp := dst + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()
	fmt.Printf("%s: cloning %s\n", r.Name, r.URL)
	if _, err := git(".", "clone", "--bare", "--quiet", r.URL, tmp); err != nil {
		return err
	}
	if err := rewind(r, tmp); err != nil {
		return err
	}
	head, err := git(tmp, "rev-parse", "refs/heads/"+r.Branch)
	if err != nil {
		return err
	}
	if r.Head == "" {
		fmt.Printf("%s: no pinned head; pin Head to %s\n", r.Name, head)
	} else if head != r.Head {
		return fmt.Errorf("%s ends at %s, not the pinned %s; upstream history changed before the cutoff", r.Branch, head, r.Head)
	}
	fmt.Printf("%s: repacking\n", r.Name)
	// One thread makes delta selection, and so the pack, deterministic for
	// a given git version.
	if _, err := git(tmp, "-c", "pack.threads=1", "repack", "-a", "-d", "-f", "-q"); err != nil {
		return err
	}
	if _, err := git(tmp, "pack-refs", "--all", "--prune"); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// rewind moves every branch to its newest first-parent commit at or before
// the cutoff, deletes branches with none, and deletes tags created after
// the cutoff or pointing at a commit made after it.
func rewind(r *bench.Repo, dir string) error {
	cutoff := r.Cutoff.Unix()
	var cmds strings.Builder
	branches, err := git(dir, "for-each-ref", "--format=%(refname)", "refs/heads")
	if err != nil {
		return err
	}
	for _, ref := range strings.Fields(branches) {
		c, err := git(dir, "rev-list", "-1", "--first-parent", "--before="+strconv.FormatInt(cutoff, 10), ref)
		if err != nil {
			return err
		}
		if c == "" {
			fmt.Fprintf(&cmds, "delete %s\n", ref)
		} else {
			fmt.Fprintf(&cmds, "update %s %s\n", ref, c)
		}
	}
	tags, err := git(dir, "for-each-ref", "--format=%(refname) %(creatordate:unix) %(*committerdate:unix)", "refs/tags")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(tags, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, d := range f[1:] {
			if t, err := strconv.ParseInt(d, 10, 64); err == nil && t > cutoff {
				fmt.Fprintf(&cmds, "delete %s\n", f[0])
				break
			}
		}
	}
	cmd := command(dir, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(cmds.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git update-ref: %w: %s", err, out)
	}
	return nil
}

func freeBytes(dir string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

func printStats(repos []*bench.Repo) {
	version, _ := git(".", "version")
	fmt.Printf("\nBuilt with %s.\n\n", version)
	fmt.Println("| Repository | Size | Pinned commit | Commits on the default branch | Commits on any branch | References | Files at `HEAD` | Objects | Pack |")
	fmt.Println("| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, r := range repos {
		dir := r.Path()
		head, err := git(dir, "rev-parse", "refs/heads/"+r.Branch)
		if err != nil {
			fmt.Printf("| %s | %s | not built | | | | | | |\n", r.Name, r.Size)
			continue
		}
		count := func(args ...string) string {
			out, err := git(dir, args...)
			if err != nil {
				return "?"
			}
			if args[0] == "rev-list" {
				return commas(out)
			}
			return commas(strconv.Itoa(len(strings.Split(out, "\n"))))
		}
		objects, size := packStats(dir)
		fmt.Printf("| [%s](%s) | %s | `%.8s` (%s) | %s | %s | %s | %s | %s | %s |\n",
			r.Name, r.URL, r.Size, head, r.Cutoff.Format("2006-01-02"),
			count("rev-list", "--count", head), count("rev-list", "--count", "--all"),
			count("for-each-ref", "--format=%(refname)"), count("ls-tree", "-r", "--name-only", head),
			objects, size)
	}
}

// packStats returns the number of packed objects and the packs' total size.
func packStats(dir string) (objects, size string) {
	out, err := git(dir, "count-objects", "-v")
	if err != nil {
		return "?", "?"
	}
	var n, kib int64
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(line, ": ")
		switch k {
		case "in-pack":
			n, _ = strconv.ParseInt(v, 10, 64)
		case "size-pack":
			kib, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	return commas(strconv.FormatInt(n, 10)), human(kib << 10)
}

// commas groups the digits of a decimal number in threes.
func commas(s string) string {
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func human(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0fMiB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%dKiB", b>>10)
}
