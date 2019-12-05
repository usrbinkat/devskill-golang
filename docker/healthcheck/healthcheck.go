// Derived closely from https://github.com/Soluto/golang-docker-healthcheck-example
// Coppied from https://medium.com/google-cloud/dockerfile-go-healthchecks-k8s-9a87d5c5b4cb

package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Expected URL as command-line argument")
		os.Exit(1)
	}
	url := os.Args[1]
	fmt.Println(url)
	if _, err := http.Get(url); err != nil {
		os.Exit(1)
	}
}
