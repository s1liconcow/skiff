package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("DATABASE_URL") == "" {
			http.Error(w, "database binding missing", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, "orders api is bound to a regional managed database writer")
	})
	_ = http.ListenAndServe(":8080", nil)
}
