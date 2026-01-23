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

	// Queue creation of all players (active + waiting)
	if db != nil {
		names := []string{gs.Red.Forward, gs.Red.Goalkeeper, gs.Blue.Forward, gs.Blue.Goalkeeper}
		names = append(names, gs.Waiting.Snapshot()...)
		db.EnqueueEnsurePlayers(names)
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

	// Enqueue upsert for the queued player
	if db != nil {
		db.EnqueueEnsurePlayers([]string{req.PlayerID})
	}

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
	var celebration *celebrationResponse
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
		// Achievement: start/reset streak if winners pair or team changed.
		if gs.StreakTeam != "red" || gs.StreakForward != gs.Red.Forward || gs.StreakGoalkeeper != gs.Red.Goalkeeper {
			gs.StreakTeam = "red"
			gs.StreakForward = gs.Red.Forward
			gs.StreakGoalkeeper = gs.Red.Goalkeeper
			// Record opponent composition at streak start (pre-rotation)
			gs.StreakOppStartF = gs.Blue.Forward
			gs.StreakOppStartG = gs.Blue.Goalkeeper
			gs.StreakAwarded = false
		}
		summary, err = rotateLoser(gs, &gs.Blue)
		if err != nil {
			gamesMu.Unlock()
			writeError(w, http.StatusConflict, err.Error())
			return
		}
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
		if gs.StreakTeam != "blue" || gs.StreakForward != gs.Blue.Forward || gs.StreakGoalkeeper != gs.Blue.Goalkeeper {
			gs.StreakTeam = "blue"
			gs.StreakForward = gs.Blue.Forward
			gs.StreakGoalkeeper = gs.Blue.Goalkeeper
			gs.StreakOppStartF = gs.Red.Forward
			gs.StreakOppStartG = gs.Red.Goalkeeper
			gs.StreakAwarded = false
		}
		summary, err = rotateLoser(gs, &gs.Red)
		if err != nil {
			gamesMu.Unlock()
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	// Check full rotation: opponent pair returned to the original pair from streak start
	if gs.StreakOppStartF != "" && gs.StreakOppStartG != "" {
		// Identify current opponent after rotation
		var oppF, oppG string
		if gs.StreakTeam == "red" {
			oppF, oppG = gs.Blue.Forward, gs.Blue.Goalkeeper
		} else {
			oppF, oppG = gs.Red.Forward, gs.Red.Goalkeeper
		}
		if samePair(gs.StreakOppStartF, gs.StreakOppStartG, oppF, oppG) {
			var winners []string
			if gs.StreakTeam == "red" {
				winners = []string{gs.Red.Forward, gs.Red.Goalkeeper}
			} else {
				winners = []string{gs.Blue.Forward, gs.Blue.Goalkeeper}
			}
			celebration = &celebrationResponse{Type: "full_rotation", Team: gs.StreakTeam, Players: winners}
			// keep same baseline so next cycles can trigger again later
		}
	}
	// Copy minimal state for DB logging, then release lock
	stateCopy := GameState{Red: gs.Red, Blue: gs.Blue}
	gamesMu.Unlock()

	// Record goal event and stats via DB queue
	if db != nil {
		db.EnqueueRecordGoal(gameID, team, stateCopy, summary, celebration != nil && celebration.Type == "full_rotation")
	}

	writeJSON(w, http.StatusOK, toGameResponseWithCelebration(gs, &summary, celebration))
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
		summaries = append(summaries, gameSummary{ID: id, Started: gs.Started})
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

	// Persist undo to DB (reverse last event and counters)
	if db != nil {
		db.EnqueueUndoLastEvent(gameID)
	}

	writeJSON(w, http.StatusOK, toGameResponse(gs, nil))
}

func toGameResponse(gs *GameState, rotation *rotationSummary) gameResponse {
	resp := gameResponse{
		Red:      gs.Red,
		Blue:     gs.Blue,
		Waiting:  gs.Waiting.Snapshot(),
		Rotation: rotation,
		Started:  gs.Started,
	}
	return resp
}

func toGameResponseWithCelebration(gs *GameState, rotation *rotationSummary, cel *celebrationResponse) gameResponse {
	resp := toGameResponse(gs, rotation)
	if cel != nil {
		resp.Celebration = cel
	}
	return resp
}

func uniqueStrings(in []string) []string {
	m := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := m[s]; ok {
			continue
		}
		m[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func samePair(a1, a2, b1, b2 string) bool {
	if a1 == "" || a2 == "" || b1 == "" || b2 == "" {
		return false
	}
	return (a1 == b1 && a2 == b2) || (a1 == b2 && a2 == b1)
}

// Remove a player from waiting queue or active slots.
func postRemovePlayer(w http.ResponseWriter, r *http.Request) {
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
	// Snapshot before mutation for undo
	gs.History = append(gs.History, snapshotGame(gs))

	// Remove from waiting if present
	removed := gs.Waiting.RemoveValue(req.PlayerID)
	// Remove from active slots if matched
	if !removed {
		if gs.Red.Forward == req.PlayerID {
			gs.Red.Forward = ""
			removed = true
		}
	}
	if !removed {
		if gs.Red.Goalkeeper == req.PlayerID {
			gs.Red.Goalkeeper = ""
			removed = true
		}
	}
	if !removed {
		if gs.Blue.Forward == req.PlayerID {
			gs.Blue.Forward = ""
			removed = true
		}
	}
	if !removed {
		if gs.Blue.Goalkeeper == req.PlayerID {
			gs.Blue.Goalkeeper = ""
			removed = true
		}
	}

	gamesMu.Unlock()

	if !removed {
		writeError(w, http.StatusNotFound, "player not found in game")
		return
	}
	writeJSON(w, http.StatusOK, toGameResponse(gs, nil))
}
