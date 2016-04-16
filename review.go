package main

import (
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
)

var reviewDirPattern = regexp.MustCompile("^([0-9]+)[.](open|merged|closed)$")

type review struct {
	Num   int
	Title string
}

func listReviews(repo string, n int) []review {
	d := filepath.Join(reviewRoot, repo)
	f, err := os.Open(d)
	if err != nil {
		if os.IsNotExist(err) {
			err := os.MkdirAll(d, 0755)
			if err != nil {
				log.Fatal(err)
			}
			return []review{}
		}
		log.Fatal(err)
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		log.Fatal(err)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) >= 50 {
		names = names[:50]
	}
	reviews := make([]review, 0, len(names))
	for _, n := range names {
		m := reviewDirPattern.FindStringSubmatch(n)
		if len(m) != 3 {
			log.Fatalf("review directory name does not match with naming rule %v: %v", repo, n)
		}
		nstr := m[1]
		n, err := strconv.Atoi(nstr)
		if err != nil {
			log.Fatal(err)
		}
		b, err := ioutil.ReadFile(filepath.Join(d, nstr+".open", "TITLE"))
		if err != nil {
			log.Fatal(err)
		}
		reviews = append(reviews, review{Num: n, Title: string(b)})
	}
	return reviews
}

func createReview(repo, title string) {
	n := lastReviewNum(repo)

	d := filepath.Join(reviewRoot, repo, strconv.Itoa(n)+".open")
	err := os.Mkdir(d, 0755)
	if err != nil {
		log.Fatal(err)
	}

	err = ioutil.WriteFile(filepath.Join(d, "TITLE"), []byte(title), 0644)
	if err != nil {
		log.Fatal(err)
	}
}

func lastReviewNum(repo string) int {
	d := filepath.Join(reviewRoot, repo)
	f, err := os.Open(d)
	if err != nil {
		log.Fatal(err)
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		log.Fatal(err)
	}
	if len(names) == 0 {
		return 1
	}
	last := -1
	for _, n := range names {
		m := reviewDirPattern.FindStringSubmatch(n)
		if len(m) != 3 {
			log.Fatalf("review directory name does not match with naming rule %v: %v", repo, n)
		}
		nstr := m[1]
		n, err := strconv.Atoi(nstr)
		if err != nil {
			log.Fatal(err)
		}
		if n > last {
			last = n
		}
	}
	return last + 1
}

// mergeReview merges nth review of the repo to some branch.
func mergeReview(repo string, n int, toB string) {
	d := filepath.Join(reviewRoot, repo, strconv.Itoa(n)+".open")
	_, err := os.Stat(d)
	if os.IsNotExist(err) {
		log.Fatalf("merge directory not found: %v", err)
	}
	out, err := ioutil.ReadFile(filepath.Join(d, "TITLE"))
	if err != nil {
		log.Fatal(err)
	}
	msg := string(out)

	// find out old branch
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Fatal(err)
	}
	oldB := string(out)

	// follow procedure will change branch.
	// prevent execute other git command on this repo.
	var m = &sync.Mutex{}
	m.Lock()
	defer m.Unlock()

	// actual process will done in {repo}.r directory.
	// then it will push to {repo} directory.
	rd := filepath.Join(repoRoot, repo+".r")
	cmd = exec.Command("git", "checkout", toB)
	cmd.Dir = rd
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("%v: %s", err, out)
	}
	cmd = exec.Command("git", "merge", "--squash", "coldmine/review/"+strconv.Itoa(n))
	cmd.Dir = rd
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("%v: %s", err, out)
	}
	cmd = exec.Command("git", "commit", "-m", msg)
	cmd.Dir = rd
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("%v: %s", err, out)
	}
	cmd = exec.Command("git", "push", toB)
	cmd.Dir = rd
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("%v: %s", err, out)
	}
	cmd = exec.Command("git", "checkout", oldB)
	cmd.Dir = rd
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("%v: %s", err, out)
	}

	os.Rename(d, filepath.Join(reviewRoot, repo, strconv.Itoa(n)+".merged"))
}

func closeReview(repo string, n int) {
	d := filepath.Join(reviewRoot, repo, strconv.Itoa(n)+".open")
	os.Rename(d, filepath.Join(reviewRoot, repo, strconv.Itoa(n)+".closed"))
}
