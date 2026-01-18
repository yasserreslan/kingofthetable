# King of the Table — Babyfoot Backend

Small HTTP backend for a foosball (babyfoot) table with in‑memory game state, FIFO waiting queue, and rotation rules. Built with Go + Gorilla Mux.

## Features
- Multiple concurrent games (server generates IDs)
- In‑memory state with mutex protection
- O(1) FIFO ring buffer for the waiting queue
- Goal rotation logic (forward -> goalkeeper, bench GK, new forward from queue)
- Undo last action per game (stack of snapshots)
- Clear JSON API and errors

## Quick Start
- Requirements: Go 1.23+
- Run
  - `go mod tidy`
  - `go run .` (listens on `:8080` by default)
  - Optional: `PORT=9090 go run .`

## Project Layout
- `main.go` — router + server bootstrap
- `handlers.go` — HTTP handlers, rotation, JSON helpers
- `store.go` — in‑memory store, helpers, ID generation
- `types.go` — core models, API DTOs, undo snapshot helpers
- `ringqueue.go` — ring buffer queue implementation

## API Summary
- POST `/games/start` — create a new game; server returns a generated `id`
- GET `/games` — list all games (id, started)
- GET `/games/{gameId}` — get full game state
- POST `/games/{gameId}/queue` — add a waiting player
- POST `/games/{gameId}/goal` — record a goal and rotate (no scoring)
- POST `/games/{gameId}/undo` — undo the last mutating action
- POST `/games/{gameId}/remove` — remove a player by `player_id` from queue or active slots
- GET `/healthz` — health check

### JSON Conventions
- Request/response bodies are JSON
- Player identifiers use `player_id` strings consistently

### Errors
- 400 — invalid body / bad `team`
- 404 — game not found
- 409 — state conflict (e.g., duplicate player, queue empty, game not started, or nothing to undo)
- Error body: `{ "error": "..." }`

## Curl Examples (localhost:8080)
- Start a new game (server generates ID)
```
curl -X POST http://localhost:8080/games/start \
  -H "Content-Type: application/json" \
  -d '{
        "red":  { "forward": "p1", "goalkeeper": "p2" },
        "blue": { "forward": "p3", "goalkeeper": "p4" },
        "waiting": ["p5", "p6", "p7"]
      }'
```
- List all games
```
curl http://localhost:8080/games
```
- Get a game
```
curl http://localhost:8080/games/<GAME_ID>
```
- Add a waiting player
```
curl -X POST http://localhost:8080/games/<GAME_ID>/queue \
  -H "Content-Type: application/json" \
  -d '{ "player_id": "p8" }'
```
- Record a goal (Blue scores → Red rotates)
```
curl -X POST http://localhost:8080/games/<GAME_ID>/goal \
  -H "Content-Type: application/json" \
  -d '{ "team": "blue" }'
```
- Record a goal (Red scores → Blue rotates)
```
curl -X POST http://localhost:8080/games/<GAME_ID>/goal \
  -H "Content-Type: application/json" \
  -d '{ "team": "red" }'
```
- Undo last action
```
curl -X POST http://localhost:8080/games/<GAME_ID>/undo
```
- Health check
```
curl http://localhost:8080/healthz
```

## Rotation Rules (recap)
- When a team concedes a goal:
  - Losing goalkeeper is benched to the back of the waiting queue
  - Losing forward moves to goalkeeper
  - First waiting player becomes new forward
- If the waiting queue is empty when a goal is posted: 409 Conflict

## Undo Semantics
- The server snapshots game state before each mutation:
  - queue add
  - goal/rotation
- `POST /games/{gameId}/undo` restores the previous snapshot (LIFO). Multiple undos are supported until history is empty.

## Notes
- This backend is stateless beyond in‑memory maps; data resets on process restart.
- All write paths are guarded by a mutex for thread safety.
- The waiting queue uses a growable ring buffer to avoid slice-churn costs.
