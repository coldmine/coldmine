package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func serveRoot(w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseFiles("index.html", "head.html", "top.html")
	if err != nil {
		log.Fatal(err)
	}
	grps, err := dirScan(repoRoot)
	if err != nil {
		log.Fatalf("scan failed: %v", err)
	}
	info := struct {
		Repo       string
		RepoGroups []*repoGroup
	}{
		Repo:       "",
		RepoGroups: grps,
	}
	err = t.Execute(w, info)
	if err != nil {
		log.Print(err)
	}
}

func serveRootAction(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	if r.Form.Get("password") != password {
		http.Error(w, "password not matched", http.StatusForbidden)
		return
	}

	add := r.Form.Get("addRepo")
	if add != "" {
		log.Printf("add repo: %v", add)
		err := addRepo(add)
		if err != nil {
			log.Print(err)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("%v", err)))
			return
		}
	}
	rm := r.Form.Get("removeRepo")
	if rm != "" {
		log.Printf("remove repo: %v", rm)
		err := removeRepo(rm)
		if err != nil {
			log.Print(err)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("%v", err)))
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func serveInit(w http.ResponseWriter, r *http.Request, repo, pth string) {
	var newIPAddr string
	ip := strings.Split(ipAddr, ":")
	if len(ip) == 1 {
		if ip[0] == "" {
			newIPAddr = "localhost"
		} else {
			newIPAddr = ipAddr
		}
	} else {
		if ip[0] == "" {
			ip[0] = "localhost"
		}
		newIPAddr = strings.Join(ip, ":")
	}
	info := struct {
		Repo string
		IP   string
	}{
		Repo: repo,
		IP:   newIPAddr,
	}
	t, err := template.ParseFiles("init.html", "head.html", "top.html")
	if err != nil {
		log.Fatal(err)
	}
	err = t.Execute(w, info)
	if err != nil {
		log.Fatal(err)
	}
}

func serveOverview(w http.ResponseWriter, r *http.Request, repo, pth string) {
	cmd := exec.Command("git", "branch")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("(%v) %s", err, out)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if string(out) == "" {
		// repository not initialized yet.
		serveInit(w, r, repo, pth)
		return
	}
	lines := strings.Split(string(out), "\n")
	lines = lines[:len(lines)-1]
	branches := make([]string, 0, len(lines))
	for _, l := range lines {
		branches = append(branches, strings.Trim(l, " \r"))
	}

	tid, err := commitTree(repo, "master")
	if err != nil {
		log.Print(err)
		http.NotFound(w, r)
		return
	}
	top, err := gitTree(repo, tid, 1)
	if err != nil {
		log.Print(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	hasReadme := false
	readme := ""
	for _, blob := range top.Blobs {
		if blob.Name == "README" {
			hasReadme = true
			b, err := blobContent(repo, blob.Id)
			if err != nil {
				log.Print(err)
				return
			}
			readme = string(b)
			break
		}
	}

	info := struct {
		Repo      string
		Branches  []string
		HasReadme bool
		Readme    string
	}{
		Repo:      repo,
		Branches:  branches,
		HasReadme: hasReadme,
		Readme:    readme,
	}
	err = overviewTmpl.Execute(w, info)
	if err != nil {
		log.Fatal(err)
	}
}

func nFilesInTree(t *Tree) int {
	n := len(t.Blobs)
	for _, tt := range t.Trees {
		n += nFilesInTree(tt)
	}
	return n
}

func serveCommit(w http.ResponseWriter, r *http.Request, repo, pth string) {
	pp := strings.Split(pth, "/")
	if pp[len(pp)-2] != "commit" {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	commit := pp[len(pp)-1]
	cmd := exec.Command("git", "show", "--pretty=format:commit %H\ntree: %T\nauthor: %an <%ae>\ndate: %ad\n\n\t%B", commit)
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("(%v) %s", err, out)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	info := struct {
		Repo     string
		Contents []string
	}{
		Repo:     repo,
		Contents: strings.SplitAfter(string(out), "\n"),
	}
	err = commitTmpl.Execute(w, info)
	if err != nil {
		log.Fatal(err)
	}
}

func serveTree(w http.ResponseWriter, r *http.Request, repo, pth string) {
	tid := strings.TrimPrefix(r.URL.Path, "/"+repo+"/tree/")
	if tid == "" {
		t, err := commitTree(repo, "master")
		if err != nil {
			log.Print(err)
			http.NotFound(w, r)
			return
		}
		tid = t
	}
	top, err := gitTree(repo, tid, -1)
	if err != nil {
		log.Print(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	info := struct {
		Repo    string
		TopTree *Tree
	}{
		Repo:    repo,
		TopTree: top,
	}
	treeTmpl.Execute(w, info)
}

func serveBlob(w http.ResponseWriter, r *http.Request, repo, pth string) {
	pp := strings.Split(pth, "/")
	if pp[len(pp)-2] != "blob" {
		log.Print("invalid blob address")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	b := pp[len(pp)-1]
	c, err := blobContent(repo, b)
	if err != nil {
		log.Print(err)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	info := struct {
		Repo    string
		Content string
	}{
		Repo:    repo,
		Content: string(c),
	}
	blobTmpl.Execute(w, info)
}

func serveLog(w http.ResponseWriter, r *http.Request, repo, pth string) {
	pp := strings.Split(r.URL.Path, "/")
	page, err := strconv.Atoi(pp[len(pp)-1])
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if page < 1 {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	// how many commits in the repo?
	cmd := exec.Command("git", "rev-list", "--count", "master")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Print("%v: (%v) %s", cmd, err, out)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	nc := string(out)
	nc = nc[:len(nc)-1]
	nCommits, err := strconv.Atoi(nc)
	if err != nil {
		log.Print(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if nCommits == 0 {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	// A page contains 15 commits.
	commitPerPage := 15

	// Check page infos.
	lastPage := ((nCommits - 1) / commitPerPage) + 1
	if page > lastPage {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	prevPage := page - 1
	if page == 1 {
		prevPage = -1 // The page not exist.
	}
	nextPage := page + 1
	if page == lastPage {
		nextPage = -1 // The page not exist.
	}

	argSkip := fmt.Sprintf("--skip=%d", commitPerPage*(page-1))
	argMaxCount := fmt.Sprintf("--max-count=%d", commitPerPage)
	cmd = exec.Command("git", "log", argSkip, argMaxCount, "--pretty=format:%H%n%ar%n%s%n")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Print("%v: (%v) %s", cmd, err, out)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	logs := make([]logEl, 0)
	for _, c := range strings.Split(string(out), "\n\n") {
		cc := strings.Split(c, "\n")
		simpleDate := strings.Join(strings.Split(strings.Replace(cc[1], ",", "", -1), " ")[:2], " ")
		logs = append(logs, logEl{ID: cc[0], Date: simpleDate, Subject: cc[2]})
	}

	info := struct {
		Repo string
		Logs []logEl
		Prev int
		Next int
	}{
		Repo: repo,
		Logs: logs,
		Prev: prevPage,
		Next: nextPage,
	}
	err = logTmpl.Execute(w, info)
	if err != nil {
		log.Fatal(err)
	}
}

func serveReviews(w http.ResponseWriter, r *http.Request, repo, pth string) {
	info := struct {
		Repo    string
		Reviews []review
	}{
		Repo:    repo,
		Reviews: listReviews(repo, 50),
	}
	err := reviewsTmpl.Execute(w, info)
	if err != nil {
		log.Fatal(err)
	}
}

func serveReviewsAction(w http.ResponseWriter, r *http.Request, repo, pth string) {
	r.ParseForm()

	if r.Form.Get("password") != password {
		http.Error(w, "password not matched", http.StatusForbidden)
		return
	}

	title := r.Form.Get("title")
	if title != "" {
		log.Printf("create a new review: %v", title)
		createReview(repo, title)
	}

	http.Redirect(w, r, "/"+repo+"/reviews/", http.StatusSeeOther)
}

func serveReview(w http.ResponseWriter, r *http.Request, repo, pth string) {
	pp := strings.Split(r.URL.Path, "/")
	nstr := pp[len(pp)-1]
	_, err := strconv.Atoi(nstr)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	reviewDirPattern := filepath.Join(reviewRoot, repo, nstr+".*")
	g, err := filepath.Glob(reviewDirPattern)
	if err != nil {
		log.Print(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if len(g) == 0 {
		http.NotFound(w, r)
		return
	} else if len(g) > 1 {
		log.Printf("should glob only one review directory: %v found - %v", len(g), g)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	reviewDir := g[0]
	ss := strings.Split(reviewDir, ".")
	reviewStatus := ss[len(ss)-1]

	// check the review branch actually pushed.
	b := "coldmine/review/" + nstr
	cmd := exec.Command("git", "branch")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("%v: (%v) %s", cmd, err, out)
	}
	find := false
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimLeft(l, "* ")
		if l == b {
			find = true
		}
	}
	if !find {
		info := struct {
			Repo   string
			Branch string
		}{
			Repo:   repo,
			Branch: b,
		}
		err = reviewInitTmpl.Execute(w, info)
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	// find merge-base commit between review branch and target branch.

	baseB := "master"
	commits, err := reviewCommits(repo, b, baseB)
	if err != nil {
		log.Fatal(err)
	}
	base := commits[0]
	if base != initialCommitID(repo) {
		// unfortunately, there seems no way for diffing against empty commit.
		base += "~1"
	}

	// generating diff
	r.ParseForm()
	if r.Form.Get("diff") != "" {
		cmd = exec.Command("git", "show", "--pretty=format:commit %H%ntree: %T%nauthor: %an <%ae>%ndate: %ad%n%n\t%B", r.Form.Get("diff"))

	} else {
		cmd = exec.Command("git", "diff", base+".."+b)
	}
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("%v: (%v) %s", cmd, err, out)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	diff := string(out)
	diffLines := strings.SplitAfter(diff, "\n")

	// serve
	info := struct {
		Repo         string
		ReviewNum    string
		ReviewStatus string
		Commits      []string
		DiffLines    []string
	}{
		Repo:         repo,
		ReviewNum:    nstr,
		ReviewStatus: reviewStatus,
		Commits:      commits,
		DiffLines:    diffLines,
	}
	err = reviewTmpl.Execute(w, info)
	if err != nil {
		log.Fatal(err)
	}
}

func serveReviewAction(w http.ResponseWriter, r *http.Request, repo, pth string) {
	r.ParseForm()
	if r.Form.Get("password") != password {
		http.Error(w, "password not matched", http.StatusForbidden)
		return
	}
	nstr := r.Form.Get("n")
	n, err := strconv.Atoi(nstr)
	if err != nil {
		log.Printf("could not get review number: %v", err)
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	act := r.Form.Get("action")
	if act == "merge" {
		mergeReview(repo, n, "coldmine/review/"+nstr, "master")
	} else if act == "close" {
		closeReview(repo, n)
	}
	redirectPath := strings.TrimSuffix(r.URL.Path, "action") + nstr
	http.Redirect(w, r, redirectPath, http.StatusSeeOther)
}
