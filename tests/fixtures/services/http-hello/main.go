package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf(`{"level":"info","message":"hello request","path":%q}`, r.URL.Path)
		fmt.Fprintln(w, "hello from skiff")
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "http_requests_total 1")
		fmt.Fprintln(w, "http_5xx_total 0")
	})
	addr := ":8080"
	if value := os.Getenv("PORT"); value != "" {
		addr = ":" + value
	}
	log.Fatal(http.ListenAndServe(addr, mux))
}
