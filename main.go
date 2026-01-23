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
	// Initialize optional DB (no-op if MYSQL_DSN unset or unreachable)
	initDB()

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
	// Leaderboard page
	r.HandleFunc("/leaderboard", func(w http.ResponseWriter, r *http.Request) {
		b, err := ioutil.ReadFile("leaderboard.html")
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
	// Players catalogue
	r.HandleFunc("/players", getPlayers).Methods(http.MethodGet)
	r.HandleFunc("/players", postPlayer).Methods(http.MethodPost)
	r.HandleFunc("/players/stats", getPlayersStats).Methods(http.MethodGet)
	// Leaderboard data
	r.HandleFunc("/leaderboard/data", getLeaderboardData).Methods(http.MethodGet)

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
