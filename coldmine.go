package main

import (
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

func main() {
	grps, err := dirScan("repo")
	if err != nil {
		log.Fatalf("initial scan failed: %v", err)
	}
	log.Print("initial scan result")
	for _, g := range grps {
		log.Print(g)
	}
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
	grps, err := dirScan("repo")
	if err != nil {
		log.Fatalf("scan failed: %v", err)
	}
	t.Execute(w, grps)
}

type repoGroup struct {
	Name  string
	Repos []string
}

func (g *repoGroup) String() string {
	return fmt.Sprintf("{Name:%v Repos:%v}", g.Name, g.Repos)
}

// dirScan scans _rootp_ directory. If the directory is not found,
// it will created.
// Max scan depth is 2. When child and grand child directory both are not
// git directories, then it will raise panic.
// That means it should one of following form.
//
//   repo/gitdir
//   repo/group/gitdir
//
func dirScan(rootp string) ([]*repoGroup, error) {
	err := os.Mkdir(rootp, 0755)
	if err != nil && !os.IsExist(err) {
		return nil, err
	}

	// map of repoGroups, will converted to sorted list eventually.
	// no-grouped (_ng_) repositories are added as last item of the list.
	grpMap := make(map[string]*repoGroup, 0)
	ng := &repoGroup{Name: ""}

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
			ng.Repos = append(ng.Repos, fi.Name())
			continue
		}

		// the child is not a git dir.
		// grand childs should be git directories.
		g := &repoGroup{Name: fi.Name()}
		grpMap[fi.Name()] = g

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
				g.Repos = append(g.Repos, dfi.Name())
				continue
			}
			return nil, errors.New("max depth reached, but not a git directory: " + ddp)
		}
	}

	// now we have map of repoGroup
	// convert it to sorted list
	keys := make([]string, 0)
	for k := range grpMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	grps := make([]*repoGroup, len(grpMap))
	for i, k := range keys {
		grps[i] = grpMap[k]
	}
	grps = append(grps, ng)

	// sort repoGroup.repos too.
	for _, g := range grps {
		sort.Strings(g.Repos)
	}

	return grps, nil
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
