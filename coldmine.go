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

var ipAddr string
var repoRoot string
var password string

func init() {
	flag.StringVar(&ipAddr, "ip", ":8080", "ip address")
	flag.StringVar(&repoRoot, "repo", "repo", "repository root directory")

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
	http.HandleFunc("/action", actionHandler)
	http.HandleFunc("/", rootHandler)
	log.Fatal(http.ListenAndServe(ipAddr, nil))
}

type Service struct {
	method      string
	pathPattern *regexp.Regexp
	serv        func(w http.ResponseWriter, r *http.Request, repo, pth string)
}

var services = []Service{
	{"GET", regexp.MustCompile("/HEAD$"), getHead},
	{"GET", regexp.MustCompile("/info/refs$"), getInfoRefs},
	{"GET", regexp.MustCompile("/objects/info/alternates$"), getTextFile},
	{"GET", regexp.MustCompile("/objects/info/http-alternates$"), getTextFile},
	{"GET", regexp.MustCompile("/objects/info/packs$"), getInfoPacks},
	{"GET", regexp.MustCompile("/objects/[0-9a-f]{2}/[0-9a-f]{38}$"), getLooseObject},
	{"GET", regexp.MustCompile("/objects/pack/pack-[0-9a-f]{40}\\.pack$"), getPackFile},
	{"GET", regexp.MustCompile("/objects/pack/pack-[0-9a-f]{40}\\.idx$"), getIdxFile},

	{"POST", regexp.MustCompile("/git-upload-pack$"), serviceUpload},
	{"POST", regexp.MustCompile("/git-receive-pack$"), serviceReceive},

	{"GET", regexp.MustCompile("/$"), serveOverview},
	{"GET", regexp.MustCompile("/tree/"), serveTree},
	{"GET", regexp.MustCompile("/blob/"), serveBlob},
	{"GET", regexp.MustCompile("/commit/"), serveCommit},
	{"GET", regexp.MustCompile("/log/"), serveLog},
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	log.Print(r.URL.Path)

	if r.URL.Path == "/" {
		serveRoot(w, r)
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
			w.WriteHeader(http.StatusMethodNotAllowed)
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

func actionHandler(w http.ResponseWriter, r *http.Request) {
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

	top, err := gitTree(repo, "master")
	if err != nil {
		log.Print(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	nFiles := nFilesInTree(top)

	info := struct {
		Repo          string
		Branches      []string
		NCommits      int
		RecentCommits []commitEl
		NFiles        int
	}{
		Repo:          repo,
		Branches:      branches,
		NCommits:      nCommits,
		RecentCommits: recentCommits,
		NFiles:        nFiles,
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
		"pickID":    pickID,
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

// pickID returns object id from "x: <id of x>\n" string.
// it used inside commit.html template.
func pickID(l string) string {
	ll := strings.Split(l, " ")
	return strings.TrimRight(ll[1], "\n")
}

type commitEl struct {
	ID    string
	Title string
}

func gitDir(d string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = d
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("(%v) %s", err, out)
	}
	return string(out) == ".\n"
}

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

// TODO: return error?
func (b *Blob) Text() string {
	cmd := exec.Command("git", "cat-file", "-p", b.Id)
	cmd.Dir = b.Repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("(%v) %s", err, out)
		return ""
	}
	return string(out)
}

func serveTree(w http.ResponseWriter, r *http.Request, repo, pth string) {
	// TODO: get tree id from _pth_. show the tree.
	top, err := gitTree(repo, "master")
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

// gitTree returns top tree of the branch.
func gitTree(repo, branch string) (*Tree, error) {
	cmd := exec.Command("git", "rev-parse", branch)
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, errors.New(fmt.Sprintf("(%v) %s", err, out))
	}
	id := out[:len(out)-1] // strip "\n"

	cmd = exec.Command("git", "cat-file", "-p", string(id))
	cmd.Dir = filepath.Join(repoRoot, repo)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return nil, errors.New(fmt.Sprintf("(%v) %s", err, out))
	}
	// find tree object id
	t := strings.Split(string(out), "\n")[0]
	if !strings.HasPrefix(t, "tree ") {
		return nil, errors.New(`commit object content not starts with "tree "`)
	}
	tid := strings.Split(t, " ")[1]

	return parseTree(repo, tid, ""), nil
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
	t.Execute(w, info)
}

type logEl struct {
	ID      string
	Date    string
	Subject string
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

// packetLine adds 4 digit hex length string to given string.
func packetLine(l string) (string, error) {
	h := strconv.FormatInt(int64(len(l)+4), 16)
	if len(h) > 4 {
		return "", errors.New("packet too long")
	}
	return strings.Repeat("0", 4-len(h)) + h + l, nil
}
