package main

import (
	"html/template"
	"strings"
)

var (
	// create templates
	overviewTmpl = template.Must(template.ParseFiles("overview.html", "head.html", "top.html"))
	treeFmap     = template.FuncMap{
		"reprTrees": reprTrees,
	}
	treeTmpl   = template.Must(template.New("tree.html").Funcs(treeFmap).ParseFiles("tree.html", "head.html", "top.html"))
	blobTmpl   = template.Must(template.ParseFiles("blob.html", "head.html", "top.html"))
	commitFmap = template.FuncMap{
		"hasPrefix": strings.HasPrefix,
		"pickID": func(l string) string {
			return strings.TrimRight(strings.Split(l, " ")[1], "\n")
		},
	}
	commitTmpl  = template.Must(template.New("commit.html").Funcs(commitFmap).ParseFiles("commit.html", "head.html", "top.html"))
	logTmpl     = template.Must(template.ParseFiles("log.html", "head.html", "top.html"))
	reviewsFmap = template.FuncMap{
		"color": func(status string) string {
			switch status {
			case "merged":
				return "blue"
			case "closed":
				return "gray"
			default:
				return "black"
			}
		},
	}
	reviewsTmpl    = template.Must(template.New("reviews.html").Funcs(reviewsFmap).ParseFiles("reviews.html", "head.html", "top.html"))
	reviewInitTmpl = template.Must(template.ParseFiles("review_init.html", "head.html", "top.html"))
	reviewFmap     = template.FuncMap{
		"hasPrefix": strings.HasPrefix,
		"pickID": func(l string) string {
			return strings.TrimRight(strings.Split(l, " ")[1], "\n")
		},
	}
	reviewTmpl = template.Must(template.New("review.html").Funcs(reviewFmap).ParseFiles("review.html", "head.html", "top.html"))
)

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

type commitEl struct {
	ID    string
	Title string
}

type logEl struct {
	ID      string
	Date    string
	Subject string
}
