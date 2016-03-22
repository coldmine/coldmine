package main

import (
	"errors"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	gitDirs, err := initialDirScan("repo")
	if err != nil {
		log.Fatalf("initial scan failed: %v", err)
	}
	log.Printf("initial scan result: %v", gitDirs)
	http.HandleFunc("/", rootHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadFile("index.html")
	if err != nil {
		log.Fatal(err)
	}
	t, err := template.New("index").Parse(string(b))
	if err != nil {
		log.Fatal(err)
	}
	t.Execute(w, nil)
}

// initialDirScan scans _rootp_ directory. If the directory is not found,
// it will created.
// Max scan depth is 2. When child and grand child directory is not a
// git directory. It will raise panic.
// That means it should one of following form.
//
//   repo/gitdir
//   repo/group/gitdir
//
func initialDirScan(rootp string) ([]string, error) {
	err := os.Mkdir(rootp, 0755)
	if err != nil && !os.IsExist(err) {
		return nil, err
	}
	paths := make([]string, 0)
	root, err := os.Open(rootp)
	if err != nil {
		return nil, err
	}
	fis, err := root.Readdir(-1)
	if err != nil {
		return nil, err
	}
	for _, fi := range fis {
		dp := filepath.Join(rootp, fi.Name())
		if !fi.IsDir() {
			return nil, errors.New("entry should a directory: " + dp)
		}
		if gitDir(dp) {
			paths = append(paths, dp)
			continue
		}
		d, err := os.Open(dp)
		if err != nil {
			return nil, err
		}
		dfis, err := d.Readdir(-1)
		if err != nil {
			return nil, err
		}
		if len(dfis) == 0 {
			return nil, errors.New("group diretory should have at least one child directory: " + dp)
		}
		for _, dfi := range dfis {
			ddp := filepath.Join(dp, dfi.Name())
			if !dfi.IsDir() {
				return nil, errors.New("entry should a directory: " + ddp)
			}
			if gitDir(ddp) {
				paths = append(paths, ddp)
				continue
			}
			return nil, errors.New("max depth reached, but not a git directory: " + ddp)
		}
	}
	return paths, nil
}

func gitDir(d string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = d
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("git error: %v", err)
	}
	return string(out) == ".\n"
}
