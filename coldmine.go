package main

import (
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TODO: template are must excuted at start of execution.

var (
	ipAddr     string
	repoRoot   string
	reviewRoot string
	password   string
)

func init() {
	flag.StringVar(&ipAddr, "ip", ":8080", "ip address")
	flag.StringVar(&repoRoot, "repo", "repo", "repository root directory")
	flag.StringVar(&reviewRoot, "review", "review", "review data root directory")

	b, err := ioutil.ReadFile("password")
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("please make 'password' file with your password.")
			os.Exit(1)
		} else {
			fmt.Printf("open password error: %v\n", err)
			os.Exit(1)
		}
	}
	password = strings.Split(string(b), "\n")[0]
	if password == "" {
		fmt.Println("password file should not empty (need password).")
		os.Exit(1)
	}
}

func main() {
	flag.Parse()

	grps, err := dirScan(repoRoot)
	if err != nil {
		log.Fatalf("initial scan failed: %v", err)
	}
	log.Print("initial scan result")
	for _, g := range grps {
		log.Print(g)
	}
	http.HandleFunc("/", rootHandler)
	log.Fatal(http.ListenAndServe(ipAddr, nil))
}

type Service struct {
	method      string
	pathPattern *regexp.Regexp
	serv        func(w http.ResponseWriter, r *http.Request, repo, pth string)
}

var services = []Service{
	// file serving
	{"GET", regexp.MustCompile("^/HEAD$"), getHead},
	{"GET", regexp.MustCompile("^/info/refs$"), getInfoRefs},
	{"GET", regexp.MustCompile("^/objects/info/alternates$"), getTextFile},
	{"GET", regexp.MustCompile("^/objects/info/http-alternates$"), getTextFile},
	{"GET", regexp.MustCompile("^/objects/info/packs$"), getInfoPacks},
	{"GET", regexp.MustCompile("^/objects/[0-9a-f]{2}/[0-9a-f]{38}$"), getLooseObject},
	{"GET", regexp.MustCompile("^/objects/pack/pack-[0-9a-f]{40}\\.pack$"), getPackFile},
	{"GET", regexp.MustCompile("^/objects/pack/pack-[0-9a-f]{40}\\.idx$"), getIdxFile},

	// git service
	{"POST", regexp.MustCompile("^/git-upload-pack$"), serviceUpload},
	{"POST", regexp.MustCompile("^/git-receive-pack$"), serviceReceive},

	// web service
	{"GET", regexp.MustCompile("^/$"), serveOverview},
	{"GET", regexp.MustCompile("^/tree/"), serveTree},
	{"GET", regexp.MustCompile("^/blob/"), serveBlob},
	{"GET", regexp.MustCompile("^/commit/"), serveCommit},
	{"GET", regexp.MustCompile("^/log/"), serveLog},
	{"GET", regexp.MustCompile("^/reviews/$"), serveReviews},
	{"POST", regexp.MustCompile("^/reviews/action$"), serveReviewsAction},
	{"GET", regexp.MustCompile("^/review/"), serveReview},
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	log.Print(r.URL.Path)

	switch r.URL.Path {
	case "/":
		serveRoot(w, r)
		return
	case "/action":
		serveRootAction(w, r)
		return
	}

	repo, subpath := splitURLPath(r.URL.Path)
	if repo == "" {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	for _, s := range services {
		if s.pathPattern.FindString(subpath) == "" {
			continue
		}
		if s.method != r.Method {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		s.serv(w, r, repo, filepath.Join(repoRoot, r.URL.Path[1:]))
		return
	}

	w.WriteHeader(http.StatusForbidden)
}

// splitURLPath split url path to repo, subpath.
// if the url not contains repo path,
// it will return "" both repo and subpath.
// root path "/" will trimmed if it exist.
func splitURLPath(p string) (string, string) {
	if strings.HasPrefix(p, "/") {
		p = p[1:]
	}
	pp := strings.Split(p, "/")
	if len(pp) < 1 {
		return "", ""
	}
	repo := pp[0]
	if gitDir(filepath.Join(repoRoot, repo)) {
		return repo, strings.TrimPrefix(p, repo)
	}
	if len(pp) < 2 {
		return "", ""
	}
	grpRepo := strings.Join(pp[0:2], "/")
	if gitDir(filepath.Join(repoRoot, grpRepo)) {
		return grpRepo, strings.TrimPrefix(p, grpRepo)
	}
	return "", ""
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

func serveRoot(w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseFiles("index.html", "top.html")
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
		// TODO: die directly? because it make program terminated eventually.
		return fmt.Errorf("repository initialzation failed: (%v) %v", err, string(out))
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

	d := filepath.Join(repoRoot, repo)
	df, err := os.Open(d)
	if os.IsNotExist(err) {
		return fmt.Errorf("repository not exist: %v", repo)
	}
	defer df.Close()

	rr := strings.Split(repo, "/")
	if len(rr) > 3 {
		return fmt.Errorf("repository path too deep: %v", repo)
	}
	if len(rr) == 1 {
		// if the directory has sub directory, then
		// it's repository group and should not deleted.
		fi, err := df.Readdir(1)
		if err != nil {
			return fmt.Errorf("couldn't read dir: %v", err)
		}
		if len(fi) != 0 && fi[0].IsDir() && gitDir(filepath.Join(d, fi[0].Name())) {
			return fmt.Errorf("group has child repository: %v", repo)
		}
	}

	err = os.RemoveAll(d)
	if err != nil {
		return fmt.Errorf("couldn't remove repository: %v: %v", repo, err)
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
		}
	}
	return nil
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
	t, err := template.ParseFiles("init.html", "top.html")
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

	cmd = exec.Command("git", "rev-list", "--count", "master")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("(%v) %s", err, out)
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

	cmd = exec.Command("git", "log", "--pretty=oneline", "-10", "master")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("(%v) %s", err, out)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	recentCommits := make([]commitEl, 0, 10)
	for _, c := range strings.Split(string(out), "\n") {
		if c == "" {
			break
		}
		cc := strings.SplitN(c, " ", 2)
		recentCommits = append(recentCommits, commitEl{ID: cc[0], Title: cc[1]})
	}

	tid, err := commitTree(repo, "master")
	if err != nil {
		log.Print(err)
		http.NotFound(w, r)
		return
	}
	top, err := gitTree(repo, tid)
	if err != nil {
		log.Print(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	nFiles := nFilesInTree(top)

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
		Repo          string
		Branches      []string
		NCommits      int
		RecentCommits []commitEl
		NFiles        int
		HasReadme     bool
		Readme        string
	}{
		Repo:          repo,
		Branches:      branches,
		NCommits:      nCommits,
		RecentCommits: recentCommits,
		NFiles:        nFiles,
		HasReadme:     hasReadme,
		Readme:        readme,
	}
	t, err := template.ParseFiles("overview.html", "top.html")
	if err != nil {
		log.Fatal(err)
	}
	err = t.Execute(w, info)
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

type commitEl struct {
	ID    string
	Title string
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
	fmap := template.FuncMap{
		"hasPrefix": strings.HasPrefix,
		"pickID": func(l string) string {
			return strings.TrimRight(strings.Split(l, " ")[1], "\n")
		},
	}
	t, err := template.New("commit.html").Funcs(fmap).ParseFiles("commit.html", "top.html")
	if err != nil {
		log.Fatal(err)
	}
	err = t.Execute(w, info)
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
	top, err := gitTree(repo, tid)
	if err != nil {
		log.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	fmap := template.FuncMap{
		"reprTrees": reprTrees,
	}
	tmpl, err := template.New("tree.html").Funcs(fmap).ParseFiles("tree.html", "top.html")
	if err != nil {
		log.Fatal(err)
	}
	info := struct {
		Repo    string
		TopTree *Tree
	}{
		Repo:    repo,
		TopTree: top,
	}
	tmpl.Execute(w, info)
}

// treeEl holds information to draw each tree element.
type treeEl struct {
	Type   string // "dir" or "file".
	Id     string
	Name   string
	Margin int
}

// reprTrees used inside of repo.html as a function of template.
func reprTrees(top *Tree, margin, incr int) []treeEl {
	reprs := make([]treeEl, 0)
	for _, b := range top.Blobs {
		reprs = append(reprs, treeEl{Type: "file", Id: b.Id, Name: b.Name, Margin: margin})
	}
	for _, t := range top.Trees {
		reprs = append(reprs, treeEl{Type: "dir", Id: t.Id, Name: t.Name, Margin: margin})
		reprs = append(reprs, reprTrees(t, margin+incr, incr)...)
	}
	return reprs
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
	t, err := template.ParseFiles("blob.html", "top.html")
	if err != nil {
		log.Fatal(err)
	}
	info := struct {
		Repo    string
		Content string
	}{
		Repo:    repo,
		Content: string(c),
	}
	t.Execute(w, info)
}

func serveLog(w http.ResponseWriter, r *http.Request, repo, pth string) {
	cmd := exec.Command("git", "log", "--pretty=format:%H%n%ar%n%s%n")
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Print(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	logs := make([]logEl, 0)
	for _, c := range strings.Split(string(out), "\n\n") {
		cc := strings.Split(c, "\n")
		simpleDate := strings.Join(strings.Split(strings.Replace(cc[1], ",", "", -1), " ")[:2], " ") + " ago"
		logs = append(logs, logEl{ID: cc[0], Date: simpleDate, Subject: cc[2]})
	}
	info := struct {
		Repo string
		Logs []logEl
	}{
		Repo: repo,
		Logs: logs,
	}
	t, err := template.ParseFiles("log.html", "top.html")
	if err != nil {
		log.Fatal(err)
	}
	err = t.Execute(w, info)
	if err != nil {
		log.Fatal(err)
	}
}

type logEl struct {
	ID      string
	Date    string
	Subject string
}

func serveReviews(w http.ResponseWriter, r *http.Request, repo, pth string) {
	info := struct {
		Repo    string
		Reviews []review
	}{
		Repo:    repo,
		Reviews: listReviews(repo, 50),
	}
	t, err := template.ParseFiles("reviews.html", "top.html")
	if err != nil {
		log.Fatal(err)
	}
	err = t.Execute(w, info)
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

	// find merge-base commit between review branch and target branch.
	b := "coldmine/review/" + nstr
	cmd := exec.Command("git", "branch", "--list", b)
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.Output()
	if err != nil {
		log.Fatal(err)
	}
	if string(out) == "" {
		info := struct {
			Repo   string
			Branch string
		}{
			Repo:   repo,
			Branch: b,
		}
		t, err := template.ParseFiles("review_init.html", "top.html")
		if err != nil {
			log.Fatal(err)
		}
		err = t.Execute(w, info)
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	cmd = exec.Command("git", "merge-base", "--octopus", b)
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("%v: (%v) %s", cmd, err, out)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	base := strings.TrimSuffix(string(out), "\n")

	// list commits the branch's last commit and merge-base commit.
	cmd = exec.Command("git", "rev-list", base+"~1.."+b)
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("%v: (%v) %s", cmd, err, out)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	commits := strings.Split(string(out), "\n")
	commits = commits[:len(commits)-1] // remove merge-base commit and empty line.

	// generating diff
	r.ParseForm()
	if r.Form.Get("diff") != "" {
		cmd = exec.Command("git", "show", "--pretty=format:commit %H%ntree: %T%nauthor: %an <%ae>%ndate: %ad%n%n\t%B", r.Form.Get("diff"))

	} else {
		cmd = exec.Command("git", "diff", base+"~1.."+b)
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
		Repo      string
		ReviewNum string
		Commits   []string
		DiffLines []string
	}{
		Repo:      repo,
		ReviewNum: nstr,
		Commits:   commits,
		DiffLines: diffLines,
	}
	fmap := template.FuncMap{
		"hasPrefix": strings.HasPrefix,
		"pickID": func(l string) string {
			return strings.TrimRight(strings.Split(l, " ")[1], "\n")
		},
	}
	t, err := template.New("review.html").Funcs(fmap).ParseFiles("review.html", "top.html")
	if err != nil {
		log.Fatal(err)
	}
	err = t.Execute(w, info)
	if err != nil {
		log.Fatal(err)
	}
}

func getHead(w http.ResponseWriter, r *http.Request, repo, pth string) {
	headerNoCache(w)
	sendFile(w, r, "text/plain", pth)
}

func getInfoRefs(w http.ResponseWriter, r *http.Request, repo, pth string) {
	r.ParseForm()
	s := r.Form.Get("service")
	if s == "git-upload-pack" || s == "git-receive-pack" {
		// smart protocol
		args := []string{"upload-pack", "--stateless-rpc", "--advertise-refs", filepath.Join(repoRoot, repo)}
		if s == "git-receive-pack" {
			args = []string{"receive-pack", "--stateless-rpc", "--advertise-refs", filepath.Join(repoRoot, repo)}
		}
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			log.Printf("(%v) %s", err, out)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		headerNoCache(w)
		w.Header().Set("Content-Type", "application/x-"+s+"-advertisement")
		p, err := packetLine("# service=" + s + "\n")
		if err != nil {
			log.Print(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(p))
		w.Write([]byte("0000")) // flushing
		w.Write(out)
	} else {
		// dumb protocol
		err := exec.Command("git", "update-server-info").Run()
		if err != nil {
			log.Print(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		headerNoCache(w)
		sendFile(w, r, "text/plain", pth)
	}
}

// packetLine adds 4 digit hex length string to given string.
func packetLine(l string) (string, error) {
	h := strconv.FormatInt(int64(len(l)+4), 16)
	if len(h) > 4 {
		return "", errors.New("packet too long")
	}
	return strings.Repeat("0", 4-len(h)) + h + l, nil
}

func getTextFile(w http.ResponseWriter, r *http.Request, repo, pth string) {
	headerNoCache(w)
	sendFile(w, r, "text/plain", pth)
}

func getInfoPacks(w http.ResponseWriter, r *http.Request, repo, pth string) {
	// TODO: pack file validation.
	headerNoCache(w)
	sendFile(w, r, "text/plain; charset=utf-8", pth)
}

func getLooseObject(w http.ResponseWriter, r *http.Request, repo, pth string) {
	headerCacheForever(w)
	sendFile(w, r, "x-git-loose-object", pth)
}

func getPackFile(w http.ResponseWriter, r *http.Request, repo, pth string) {
	headerCacheForever(w)
	sendFile(w, r, "x-git-packed-objects", pth)
}

func getIdxFile(w http.ResponseWriter, r *http.Request, repo, pth string) {
	headerCacheForever(w)
	sendFile(w, r, "x-git-packed-objects-toc", pth)
}

func serviceUpload(w http.ResponseWriter, r *http.Request, repo, pth string) {
	service(w, r, "upload-pack", repo, pth)
}

func serviceReceive(w http.ResponseWriter, r *http.Request, repo, pth string) {
	service(w, r, "receive-pack", repo, pth)
}

func service(w http.ResponseWriter, r *http.Request, s, repo, pth string) {
	w.Header().Set("Content-Type", "application/x-git-"+s+"-result")

	cmd := exec.Command("git", s, "--stateless-rpc", filepath.Join(repoRoot, repo))

	in, err := cmd.StdinPipe()
	if err != nil {
		log.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		log.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	err = cmd.Start()
	if err != nil {
		log.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Print(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	in.Write(body)
	io.Copy(w, out)
	cmd.Wait()
}

func headerNoCache(w http.ResponseWriter) {
	w.Header().Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
}

func headerCacheForever(w http.ResponseWriter) {
	now := time.Now().Unix()
	w.Header().Set("Date", fmt.Sprintf("%v", now))
	w.Header().Set("Expires", fmt.Sprintf("%v", now+31536000))
	w.Header().Set("Cache-Control", "public, max-age=31536000")
}

func sendFile(w http.ResponseWriter, r *http.Request, typ string, pth string) {
	f, err := os.Stat(pth)
	if err != nil {
		if os.IsNotExist(err) {
			w.WriteHeader(http.StatusNotFound)
			return
		} else {
			log.Print(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Content-Length", fmt.Sprintf("%v", f.Size()))
	w.Header().Set("Last-Modified", f.ModTime().Format(http.TimeFormat))
	http.ServeFile(w, r, pth)
}
