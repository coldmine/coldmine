package main

import (
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

type Service struct {
	method      string
	pathPattern *regexp.Regexp
	serv        func(w http.ResponseWriter, r *http.Request, repo, pth string)
}

var services = []Service{
	// git service
	{"GET", regexp.MustCompile("^/HEAD$"), getHead},
	{"GET", regexp.MustCompile("^/info/refs$"), getInfoRefs},
	{"GET", regexp.MustCompile("^/objects/info/alternates$"), getTextFile},
	{"GET", regexp.MustCompile("^/objects/info/http-alternates$"), getTextFile},
	{"GET", regexp.MustCompile("^/objects/info/packs$"), getInfoPacks},
	{"GET", regexp.MustCompile("^/objects/[0-9a-f]{2}/[0-9a-f]{38}$"), getLooseObject},
	{"GET", regexp.MustCompile("^/objects/pack/pack-[0-9a-f]{40}\\.pack$"), getPackFile},
	{"GET", regexp.MustCompile("^/objects/pack/pack-[0-9a-f]{40}\\.idx$"), getIdxFile},
	{"POST", regexp.MustCompile("^/git-upload-pack$"), serviceUpload},
	{"POST", regexp.MustCompile("^/git-receive-pack$"), serviceReceive},

	// web service
	{"GET", regexp.MustCompile("^/$"), serveOverview},
	{"GET", regexp.MustCompile("^/tree/"), serveTree},
	{"GET", regexp.MustCompile("^/blob/"), serveBlob},
	{"GET", regexp.MustCompile("^/commit/"), serveCommit},
	{"GET", regexp.MustCompile("^/log/"), serveLog},
	{"POST", regexp.MustCompile("^/reviews/action$"), serveReviewsAction},
	{"GET", regexp.MustCompile("^/reviews/$"), serveReviews},
	{"POST", regexp.MustCompile("^/review/action$"), serveReviewAction},
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
