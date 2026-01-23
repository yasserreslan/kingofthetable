package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// GET /players?query=foo&limit=20
func getPlayers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	limitStr := strings.TrimSpace(r.URL.Query().Get("limit"))
	limit := 20
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}
	if db == nil {
		writeError(w, http.StatusNotImplemented, "database not configured")
		return
	}
	res, err := db.SearchPlayers(r.Context(), q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type createPlayerRequest struct {
	Name string `json:"name"`
}

// POST /players {"name": "alice"}
// Upserts a player by name and returns the record.
func postPlayer(w http.ResponseWriter, r *http.Request) {
	if db == nil {
		writeError(w, http.StatusNotImplemented, "database not configured")
		return
	}
	var req createPlayerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	// Enqueue creation; return minimal response
	db.EnqueueEnsurePlayers([]string{name})
	writeJSON(w, http.StatusOK, Player{ID: 0, Name: name})
}

// GET /leaderboard/data?limit=50
func getLeaderboardData(w http.ResponseWriter, r *http.Request) {
	if db == nil {
		writeError(w, http.StatusNotImplemented, "database not configured")
		return
	}
	limit := 50
	if s := strings.TrimSpace(r.URL.Query().Get("limit")); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 1000 {
			limit = v
		}
	}
	players, err := db.SearchPlayers(r.Context(), "", limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, players)
}
