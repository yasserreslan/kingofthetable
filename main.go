package main

import (
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

func main() {
	r := mux.NewRouter()
	// Serve the interactive UI at /babyfoot
	r.HandleFunc("/babyfoot", func(w http.ResponseWriter, r *http.Request) {
		b, err := ioutil.ReadFile("babyfoot.html")
		if err != nil {
			http.Error(w, "UI not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}).Methods(http.MethodGet)
	r.HandleFunc("/games", getGames).Methods(http.MethodGet)
	r.HandleFunc("/games/start", postStartNewGame).Methods(http.MethodPost)
	r.HandleFunc("/games/{gameId}", getGame).Methods(http.MethodGet)
	r.HandleFunc("/games/{gameId}/queue", postQueue).Methods(http.MethodPost)
	r.HandleFunc("/games/{gameId}/goal", postGoal).Methods(http.MethodPost)
	r.HandleFunc("/games/{gameId}/undo", postUndo).Methods(http.MethodPost)
	r.HandleFunc("/games/{gameId}/remove", postRemovePlayer).Methods(http.MethodPost)

	// Simple health check
	r.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}).Methods(http.MethodGet)

	addr := ":8080"
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		if _, err := strconv.Atoi(port); err == nil {
			addr = ":" + port
		}
	}
	log.Printf("kingofthetable listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, r))
}
