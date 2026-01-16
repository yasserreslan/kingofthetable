package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// JSON helpers
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Core rotation logic
func rotateLoser(gs *GameState, loser *TeamState) (rotationSummary, error) {
	if gs.Waiting.Len() == 0 {
		return rotationSummary{}, errors.New("waiting queue empty; cannot rotate losing team")
	}
	benched := loser.Goalkeeper
	gs.Waiting.Enqueue(benched)
	moved := loser.Forward
	loser.Goalkeeper = moved
	newForward, _ := gs.Waiting.Dequeue()
	loser.Forward = newForward
	return rotationSummary{Benched: benched, MovedToGoalkeeper: moved, NewForward: newForward}, nil
}

// Handlers
// Start a new game and let the server allocate an ID.
func postStartNewGame(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if containsEmptyIDs(req.Red.Forward, req.Red.Goalkeeper, req.Blue.Forward, req.Blue.Goalkeeper) {
		writeError(w, http.StatusBadRequest, "empty player_id in active slots")
		return
	}
	for _, id := range req.Waiting {
		if strings.TrimSpace(id) == "" {
			writeError(w, http.StatusBadRequest, "empty player_id in waiting queue")
			return
		}
	}

	gs := &GameState{
		Red:     req.Red,
		Blue:    req.Blue,
		Waiting: NewRingQueue(max(8, len(req.Waiting))),
		Score:   Score{Red: 0, Blue: 0},
		Started: true,
		History: nil,
	}
	for _, id := range req.Waiting {
		gs.Waiting.Enqueue(id)
	}
	if dup, ok := hasDuplicate(collectAllIDs(gs)); ok {
		writeError(w, http.StatusConflict, "duplicate player_id: "+dup)
		return
	}

	gamesMu.Lock()
	id := newGameIDLocked()
	games[id] = gs
	gamesMu.Unlock()

	writeJSON(w, http.StatusOK, startNewGameResponse{ID: id, State: toGameResponse(gs, nil)})
}

func postQueue(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	gameID := vars["gameId"]

	var req queueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.PlayerID = strings.TrimSpace(req.PlayerID)
	if req.PlayerID == "" {
		writeError(w, http.StatusBadRequest, "player_id is required")
		return
	}

	gamesMu.Lock()
	gs, ok := games[gameID]
	if !ok {
		gamesMu.Unlock()
		writeError(w, http.StatusNotFound, "game not found")
		return
	}
	if playerExistsInGame(gs, req.PlayerID) {
		gamesMu.Unlock()
		writeError(w, http.StatusConflict, "player_id already exists in game")
		return
	}
	// Snapshot before mutation for undo
	gs.History = append(gs.History, snapshotGame(gs))
	gs.Waiting.Enqueue(req.PlayerID)
	gamesMu.Unlock()

	writeJSON(w, http.StatusOK, toGameResponse(gs, nil))
}

func postGoal(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	gameID := vars["gameId"]

	var req goalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	team := strings.ToLower(strings.TrimSpace(req.Team))
	if team != "red" && team != "blue" {
		writeError(w, http.StatusBadRequest, "team must be 'red' or 'blue'")
		return
	}

	gamesMu.Lock()
	gs, ok := games[gameID]
	if !ok {
		gamesMu.Unlock()
		writeError(w, http.StatusNotFound, "game not found")
		return
	}

	var summary rotationSummary
	var err error
	if team == "red" {
		// Red scores, Blue loses
		if !gs.Started {
			gamesMu.Unlock()
			writeError(w, http.StatusConflict, "game not started")
			return
		}
		if gs.Waiting.Len() == 0 {
			gamesMu.Unlock()
			writeError(w, http.StatusConflict, "waiting queue empty; cannot rotate losing team")
			return
		}
		// Snapshot before mutation for undo
		gs.History = append(gs.History, snapshotGame(gs))
		summary, err = rotateLoser(gs, &gs.Blue)
		if err != nil {
			gamesMu.Unlock()
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		gs.Score.Red++
	} else {
		// Blue scores, Red loses
		if !gs.Started {
			gamesMu.Unlock()
			writeError(w, http.StatusConflict, "game not started")
			return
		}
		if gs.Waiting.Len() == 0 {
			gamesMu.Unlock()
			writeError(w, http.StatusConflict, "waiting queue empty; cannot rotate losing team")
			return
		}
		// Snapshot before mutation for undo
		gs.History = append(gs.History, snapshotGame(gs))
		summary, err = rotateLoser(gs, &gs.Red)
		if err != nil {
			gamesMu.Unlock()
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		gs.Score.Blue++
	}
	gamesMu.Unlock()

	writeJSON(w, http.StatusOK, toGameResponse(gs, &summary))
}

func getGame(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	gameID := vars["gameId"]

	gamesMu.RLock()
	gs, ok := games[gameID]
	gamesMu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "game not found")
		return
	}
	writeJSON(w, http.StatusOK, toGameResponse(gs, nil))
}

func getGames(w http.ResponseWriter, r *http.Request) {
	gamesMu.RLock()
	summaries := make([]gameSummary, 0, len(games))
	for id, gs := range games {
		summaries = append(summaries, gameSummary{ID: id, Started: gs.Started, Score: gs.Score})
	}
	gamesMu.RUnlock()
	writeJSON(w, http.StatusOK, summaries)
}

// Undo the last state-changing action for a game.
func postUndo(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	gameID := vars["gameId"]

	gamesMu.Lock()
	gs, ok := games[gameID]
	if !ok {
		gamesMu.Unlock()
		writeError(w, http.StatusNotFound, "game not found")
		return
	}
	if len(gs.History) == 0 {
		gamesMu.Unlock()
		writeError(w, http.StatusConflict, "no actions to undo")
		return
	}
	last := gs.History[len(gs.History)-1]
	gs.History = gs.History[:len(gs.History)-1]
	applySnapshot(gs, last)
	gamesMu.Unlock()

	writeJSON(w, http.StatusOK, toGameResponse(gs, nil))
}

func toGameResponse(gs *GameState, rotation *rotationSummary) gameResponse {
	resp := gameResponse{
		Red:      gs.Red,
		Blue:     gs.Blue,
		Waiting:  gs.Waiting.Snapshot(),
		Score:    gs.Score,
		Rotation: rotation,
		Started:  gs.Started,
	}
	return resp
}
