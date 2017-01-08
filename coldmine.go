package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
)

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
	log.Printf("binding to %v", ipAddr)
	log.Fatal(http.ListenAndServe(ipAddr, nil))
}
