package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Tree struct {
	Repo  string
	Id    string
	Name  string
	Trees []*Tree
	Blobs []*Blob
}

func (t *Tree) String() string {
	return fmt.Sprintf("tree: %v %v", t.Id[:8], t.Name)
}

type Blob struct {
	Repo string
	Id   string
	Name string
}

func (b *Blob) String() string {
	return fmt.Sprintf("blob: %v %v", b.Id[:8], b.Name)
}

// gitDir checks whether the _d_ is git directory, or not.
// if not found the path, it will return false.
// any other error makes it fatal.
func gitDir(d string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = d
	out, err := cmd.CombinedOutput()
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		log.Fatalf("(%v) %s", err, out)
	}
	return string(out) == ".\n"
}

func lastUpdate(repo string) string {
	cmd := exec.Command("git", "log", "--pretty=format:%ar", "-1")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

// commitTree find tree id from the commit id.
// the commit id _c_ will always rev-parsed.
func commitTree(repo, c string) (string, error) {
	cmd := exec.Command("git", "cat-file", "-t", c)
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.New(fmt.Sprintf("(%v) %s", err, out))
	}
	if string(out) != "commit\n" {
		return "", errors.New(fmt.Sprintf("%v is not a commit id", c))
	}
	cmd = exec.Command("git", "rev-parse", c)
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return "", errors.New(fmt.Sprintf("(%v) %s", err, out))
	}
	id := out[:len(out)-1] // strip "\n"
	cmd = exec.Command("git", "cat-file", "-p", string(id))
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return "", errors.New(fmt.Sprintf("(%v) %s", err, out))
	}
	// find tree object id
	t := strings.Split(string(out), "\n")[0]
	if !strings.HasPrefix(t, "tree ") {
		return "", errors.New(`commit object content not starts with "tree "`)
	}
	return strings.Split(t, " ")[1], nil
}

// gitTree returns parsed *Tree object of given tree id.
// the *Tree object contains all the child data.
func gitTree(repo, t string) (*Tree, error) {
	cmd := exec.Command("git", "cat-file", "-t", t)
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("(%v) %s", err, out)
	}
	if string(out) != "tree\n" {
		return nil, fmt.Errorf("%v is not a tree id of %v", t, repo)
	}
	return parseTree(repo, t, ""), nil
}

// parseTree parses tree hierarchy with given id and return a top tree.
func parseTree(repo, id string, name string) *Tree {
	top := &Tree{Repo: repo, Id: id, Name: name}

	cmd := exec.Command("git", "cat-file", "-p", string(id))
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("(%v) %s", err, out)
	}
	for _, l := range strings.Split(string(out), "\n") {
		// the line looks like this.
		// 100644 blob e6e777ec163436193a336a561cfbf57c3b06ccaa	README.md
		// 100644 tree 8094086457b9e41a0c10ee3fef479056542da579	someDir
		if l == "" {
			// maybe last line.
			continue
		}
		ll := strings.Split(l, "\t")
		cinfos := strings.Split(ll[0], " ")
		ctype := cinfos[1]
		cid := cinfos[2]
		cname := ll[1]
		if ctype == "tree" {
			top.Trees = append(top.Trees, parseTree(repo, cid, cname))
		} else {
			top.Blobs = append(top.Blobs, &Blob{Repo: repo, Id: cid, Name: cname})
		}
	}
	return top
}

func blobContent(repo, b string) ([]byte, error) {
	cmd := exec.Command("git", "cat-file", "-t", b)
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("(%v) %s", err, out)
	}
	if string(out) != "blob\n" {
		return nil, fmt.Errorf("repo '%v' don't have blob '%v'", repo, b)
	}
	cmd = exec.Command("git", "cat-file", "-p", b)
	cmd.Dir = filepath.Join(repoRoot, repo)
	c, _ := cmd.Output()
	return c, nil
}

// initialCommitID will return initial commit id of the repo.
// it will return empty string if the repo don't have any commit yet.
func initialCommitID(repo string) string {
	cmd := exec.Command("git", "rev-list", "--all", "--reverse")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Print("%v: (%v) %s", cmd, err, out)
		return ""
	}
	return strings.Split(string(out), "\n")[0]
}

func currentBranch(repo string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Print("%v: (%v) %s", cmd, err, out)
		return ""
	}
	return strings.TrimSuffix(string(out), "\n")
}
