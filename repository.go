package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type repoInfo struct {
	Name    string
	Updated string
}

type repoGroup struct {
	Name  string
	Repos []repoInfo
}

func (g *repoGroup) String() string {
	return fmt.Sprintf("{Name:%v Repos:%v}", g.Name, g.Repos)
}

// dirScan scans _rootp_ directory. If the directory is not found,
// it will created.
// Max scan depth is 2. When child and grand child directory both are not
// git directories, then it will return error.
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
		if strings.HasSuffix(dp, ".r") {
			continue
		}
		if gitDir(dp) {
			ng.Repos = append(ng.Repos, repoInfo{Name: fi.Name(), Updated: lastUpdate(dp)})
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
			if strings.HasSuffix(ddp, ".r") {
				continue
			}
			if gitDir(ddp) {
				g.Repos = append(g.Repos, repoInfo{Name: dfi.Name(), Updated: lastUpdate(ddp)})
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
		sort.Sort(byName(g.Repos))
	}

	return grps, nil
}

type byName []repoInfo

func (s byName) Len() int {
	return len(s)
}

func (s byName) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byName) Less(i, j int) bool {
	return s[i].Name < s[j].Name
}

func addRepo(repo string) error {
	if repo == "" {
		return errors.New("no repository name given.")
	}
	if strings.HasPrefix(repo, "/") {
		return errors.New("no permission for that!")
	}
	if len(strings.Split(repo, "/")) > 3 {
		return fmt.Errorf("repository path too deep: %v", repo)
	}
	if strings.Contains(repo, ".") {
		return fmt.Errorf("repository name should not have dot(.): %v", repo)
	}

	d := filepath.Join(repoRoot, repo)
	_, err := os.Stat(d)
	if err == nil {
		return fmt.Errorf("repository already exist: %v", repo)
	}
	err = os.MkdirAll(d, 0755)
	if err != nil {
		return fmt.Errorf("couldn't make repository: %v: %v", repo, err)
	}
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = d
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("repository initialzation failed: (%v) %v", err, string(out))
	}

	// create non-bare repo for review.
	// it will be used to merge review branch to destination branch.
	rd := d + ".r"
	_, err = os.Stat(rd)
	if err == nil {
		return fmt.Errorf("review repository already exist: %v", repo)
	}
	err = os.MkdirAll(rd, 0755)
	if err != nil {
		return fmt.Errorf("couldn't make repository: %v: %v", repo, err)
	}
	cmd = exec.Command("git", "init")
	cmd.Dir = rd
	out, err = cmd.Output()
	if err != nil {
		log.Fatalf("review repository initialzation failed: (%v) %v", err, string(out))
	}
	cmd = exec.Command("git", "remote", "add", "origin", "../"+filepath.Base(repo))
	cmd.Dir = rd
	out, err = cmd.Output()
	if err != nil {
		log.Fatalf("review repository setup origin failed: (%v) %v", err, string(out))
	}

	// setup after-receive hooks, for auto pull to review direcotry.
	hook := fmt.Sprintf(`#!/bin/bash
unset $(git rev-parse --local-env-vars)
while read oldrev newrev refname
do
	branch=$(git rev-parse --symbolic --abbrev-ref $refname)
	cd ../%v
	if [ "$branch" == "master" ]; then
		git pull origin master
	else
		git fetch origin --update-head-ok $branch
		git branch -f $branch origin/$branch
	fi
	cd $OLDPWD
done
`, filepath.Base(rd))
	err = ioutil.WriteFile(filepath.Join(d, "hooks", "post-receive"), []byte(hook), 0755)
	if err != nil {
		log.Fatal(err)
	}

	return nil
}

func removeRepo(repo string) error {
	if repo == "" {
		return errors.New("no repository name given.")
	}
	if strings.HasPrefix(repo, "/") {
		return errors.New("no permission for that!")
	}
	rr := strings.Split(repo, "/")
	if len(rr) > 3 {
		return fmt.Errorf("repository path too deep: %v", repo)
	}

	d := filepath.Join(repoRoot, repo)
	if len(rr) == 1 {
		// if the directory has sub directory, then
		// it's repository group and should not deleted.
		df, err := os.Open(d)
		if os.IsNotExist(err) {
			return fmt.Errorf("repository not exist: %v", repo)
		}
		defer df.Close()
		fi, err := df.Readdir(1)
		if err != nil {
			return fmt.Errorf("couldn't read dir: %v", err)
		}
		if len(fi) != 0 && fi[0].IsDir() && gitDir(filepath.Join(d, fi[0].Name())) {
			return fmt.Errorf("group has child repository: %v", repo)
		}
	}
	// we should remove 3 directory related with this repo.
	err := os.RemoveAll(d)
	if err != nil {
		return fmt.Errorf("couldn't remove repository: %v: %v", repo, err)
	}
	err = os.RemoveAll(d + ".r")
	if err != nil {
		return fmt.Errorf("couldn't remove review repository: %v: %v", repo, err)
	}
	err = os.RemoveAll(filepath.Join(reviewRoot, repo))
	if err != nil {
		return fmt.Errorf("couldn't remove review data directory: %v: %v", repo, err)
	}

	if len(rr) == 2 {
		// after remove sub directory of group, check group directory.
		// if no sub directory exist in group, remove it together.
		pd := filepath.Join(repoRoot, rr[0])
		pdf, err := os.Open(pd)
		if err != nil {
			return err
		}
		defer pdf.Close()
		fi, err := pdf.Readdir(-1)
		if err != nil {
			return fmt.Errorf("couldn't read dir: %v", err)
		}
		if len(fi) == 0 {
			err = os.Remove(pd)
			if err != nil {
				return fmt.Errorf("couldn't remove repository: %v: %v", repo, err)
			}
			err = os.Remove(filepath.Join(reviewRoot, rr[0]))
			if err != nil {
				return fmt.Errorf("couldn't remove review group directory: %v: %v", repo, err)
			}
		}
	}
	return nil
}
