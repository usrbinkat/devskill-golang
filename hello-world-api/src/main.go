package main

import (
	"os"                     // Using as part of logging strategy
	"io"                     // Using for logging functions
	"io/ioutil"              // Using for logging functions
	"log"                    // Used to log events 'ie: when the server exits' or Logging is called in func
	"fmt"                    // Used to print to STDOUT aka the Console
    "encoding/json"          // Used for json formatting & responses
	"net/http"               // Presents HTTP requests and responses & server
	"github.com/gorilla/mux" // API router, responsible for handling requests
	"os/exec"
)

var (
	Trace    *log.Logger
	Info     *log.Logger
	Warning  *log.Logger
	Error    *log.Logger
)

func Init(
	traceHandle io.Writer,
	infoHandle io.Writer,
	warningHandle io.Writer,
	errorHandle io.Writer) {

		Trace = log.New(traceHandle,
			"TRACE: ",
				log.Ldate|log.Ltime|log.Lshortfile)

		Info = log.New(infoHandle,
			"INFO: ",
			    log.Ldate|log.Ltime|log.Lshortfile)

		Warning = log.New(warningHandle,
			"WARN: ",
			    log.Ldate|log.Ltime|log.Lshortfile)

		Error = log.New(errorHandle,
			"ERROR: ",
				log.Ldate|log.Ltime|log.Lshortfile)
}

func ping_dns() {
	cmd := "ping -c 1 8.8.8.8"
	exec.Command(cmd).Output()
}
func main() {
	// Define a router for http requests to call other Go functions
    r := mux.NewRouter()
	// Define '/' requests to call from / path
	r.HandleFunc("/", HomeHandler).Methods("GET")
	// Define 'healthcheck' requests to call from /healthcheck path
	r.HandleFunc("/healthcheck", healthCheck).Methods("GET")
	// Define simple 'GET' route
	r.HandleFunc("/message", handleQryMessage).Methods("GET")
	// Adding path parameter for route
	r.HandleFunc("/m/{msg}", handleUrlMessage).Methods("GET")
    http.Handle("/", r)
                                              
	// Print msg to STDOUT to confirm server is running
	fmt.Println("Running! -- CCIO MicroCloud REST API Va01.001")
	//Set router to listen on a port
	log.Fatal(http.ListenAndServe(":3000", r))
}

func handleQryMessage(w http.ResponseWriter, r *http.Request) {
	vars := r.URL.Query()
	message := vars.Get("msg")

	json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func handleUrlMessage(w http.ResponseWriter, r *http.Request) {
	Init(ioutil.Discard, os.Stdout, os.Stdout, os.Stderr)
	vars := mux.Vars(r)
	message := vars["msg"]

	json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// 1. create http.ResponseWriter to respond to client requests
// 2. add an http.Request (r) for more intelligent client request handling
func healthCheck(w http.ResponseWriter, r *http.Request) {
	Info.Println("Responded to browser health check request")
	log.Println("Say 'hi' to Browser!")
	json.NewEncoder(w).Encode("Marco.. Polo.. Health check A-OK! Alive and well :)")
}

func HomeHandler(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode("Ping your mutha fuqin face yo!")
	log.Println("ping yo face!")
	ping_dns()
}
/*
Refrences:
 - https://golang.org/doc/code.html
 - https://golang.org/pkg/log/
 - https://www.ardanlabs.com/blog/2013/11/using-log-package-in-go.html
 - https://github.com/gorilla/mux#examples
TODO:
 - https://dev.to/aspittel/how-i-built-an-api-with-mux-go-postgresql-and-gorm-5ah8
 - https://semaphoreci.com/community/tutorials/building-and-testing-a-rest-api-in-go-with-gorilla-mux-and-postgresql
 - https://medium.com/@kelvin_sp/building-and-testing-a-rest-api-in-golang-using-gorilla-mux-and-mysql-1f0518818ff6
 - https://github.com/kelseyhightower/ipxed/blob/master/main.go
 - https://github.com/Aris-haryanto/Simple-CRUD-Web-Applications-With-Golang-Mysql/blob/master/wiki.go
 - https://yen3.github.io/posts/2017/lxd_basic_usage_note/
 - https://github.com/CanonicalLtd/multipass/blob/master/snap/snapcraft.yaml
 */
