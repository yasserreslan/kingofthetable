package main

type TeamState struct {
	Forward    string `json:"forward"`
	Goalkeeper string `json:"goalkeeper"`
}

type Score struct {
	Red  int `json:"red"`
	Blue int `json:"blue"`
}

type GameState struct {
	Red     TeamState
	Blue    TeamState
	Waiting *RingQueue
	Score   Score
	Started bool
	History []GameSnapshot
}

// Snapshot used for undo support.
type GameSnapshot struct {
	Red     TeamState
	Blue    TeamState
	Waiting []string
	Score   Score
	Started bool
}

func snapshotGame(gs *GameState) GameSnapshot {
	return GameSnapshot{
		Red:     gs.Red,
		Blue:    gs.Blue,
		Waiting: gs.Waiting.Snapshot(),
		Score:   gs.Score,
		Started: gs.Started,
	}
}

func applySnapshot(gs *GameState, snap GameSnapshot) {
	gs.Red = snap.Red
	gs.Blue = snap.Blue
	q := NewRingQueue(max(8, len(snap.Waiting)))
	for _, id := range snap.Waiting {
		q.Enqueue(id)
	}
	gs.Waiting = q
	gs.Score = snap.Score
	gs.Started = snap.Started
}

// Request/response models
type resetRequest struct {
	Red     TeamState `json:"red"`
	Blue    TeamState `json:"blue"`
	Waiting []string  `json:"waiting"`
}

type queueRequest struct {
	PlayerID string `json:"player_id"`
}

type goalRequest struct {
	Team string `json:"team"`
}

type rotationSummary struct {
	Benched           string `json:"benched"`
	MovedToGoalkeeper string `json:"moved_to_goalkeeper"`
	NewForward        string `json:"new_forward"`
}

type gameResponse struct {
	Red      TeamState        `json:"red"`
	Blue     TeamState        `json:"blue"`
	Waiting  []string         `json:"waiting"`
	Score    Score            `json:"score"`
	Rotation *rotationSummary `json:"rotation,omitempty"`
	Started  bool             `json:"started"`
}

type gameSummary struct {
	ID      string `json:"id"`
	Started bool   `json:"started"`
	Score   Score  `json:"score"`
}

type startNewGameResponse struct {
	ID    string       `json:"id"`
	State gameResponse `json:"state"`
}
