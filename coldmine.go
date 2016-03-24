package main

import (
	"errors"
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

const (
	repoRoot = "repo"
)

func main() {
	grps, err := dirScan(repoRoot)
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

type Service struct {
	method      string
	pathPattern *regexp.Regexp
	serv        func(w http.ResponseWriter, r *http.Request, repo, pth string)
}

var services = []Service{
	{"GET", regexp.MustCompile("/(.+)/HEAD$"), getHead},
	{"GET", regexp.MustCompile("/(.+)/info/refs$"), getInfoRefs},
	{"GET", regexp.MustCompile("/(.+)/objects/info/alternates$"), getTextFile},
	{"GET", regexp.MustCompile("/(.+)/objects/info/http-alternates$"), getTextFile},
	{"GET", regexp.MustCompile("/(.+)/objects/info/packs$"), getInfoPacks},
	{"GET", regexp.MustCompile("/(.+)/objects/[0-9a-f]{2}/[0-9a-f]{38}$"), getLooseObject},
	{"GET", regexp.MustCompile("/(.+)/objects/pack/pack-[0-9a-f]{40}\\.pack$"), getPackFile},
	{"GET", regexp.MustCompile("/(.+)/objects/pack/pack-[0-9a-f]{40}\\.idx$"), getIdxFile},

	{"POST", regexp.MustCompile("/(.+)/git-upload-pack$"), serviceUpload},
	{"POST", regexp.MustCompile("/(.+)/git-receive-pack$"), serviceReceive},
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	log.Print(r.URL.Path)

	if r.URL.Path == "/" {
		serveRoot(w, r)
		return
	}

	for _, s := range services {
		m := s.pathPattern.FindStringSubmatch(r.URL.Path)
		if m == nil {
			continue
		}
		repo := m[1]
		if s.method != r.Method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.serv(w, r, filepath.Join(repoRoot, repo), filepath.Join(repoRoot, r.URL.Path[1:]))
		return
	}

	w.WriteHeader(http.StatusForbidden)
}

func serveRoot(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadFile("index.html")
	if err != nil {
		log.Fatal(err)
	}
	t, err := template.New("index").Parse(string(b))
	if err != nil {
		log.Fatal(err)
	}
	grps, err := dirScan(repoRoot)
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

func getHead(w http.ResponseWriter, r *http.Request, repo, pth string) {
	headerNoCache(w)
	sendFile(w, r, "text/plain", pth)
}

func getInfoRefs(w http.ResponseWriter, r *http.Request, repo, pth string) {
	r.ParseForm()
	s := r.Form.Get("service")
	if s == "git-upload-pack" || s == "git-receive-pack" {
		// smart protocol
		args := []string{"upload-pack", "--stateless-rpc", "--advertise-refs", repo}
		if s == "git-receive-pack" {
			args = []string{"receive-pack", "--stateless-rpc", "--advertise-refs", repo}
		}
		out, err := exec.Command("git", args...).Output()
		if err != nil {
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

	cmd := exec.Command("git", s, "--stateless-rpc", repo)

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
