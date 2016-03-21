package main

import (
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
)

func main() {
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
