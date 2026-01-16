package main

import (
	crand "crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// In-memory store
var (
	gamesMu sync.RWMutex
	games   = map[string]*GameState{}
)

func containsEmptyIDs(ids ...string) bool {
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return true
		}
	}
	return false
}

func collectAllIDs(gs *GameState) []string {
	ids := []string{gs.Red.Forward, gs.Red.Goalkeeper, gs.Blue.Forward, gs.Blue.Goalkeeper}
	ids = append(ids, gs.Waiting.Snapshot()...)
	return ids
}

func hasDuplicate(ids []string) (string, bool) {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			return id, true
		}
		seen[id] = struct{}{}
	}
	return "", false
}

func playerExistsInGame(gs *GameState, id string) bool {
	if gs.Red.Forward == id || gs.Red.Goalkeeper == id || gs.Blue.Forward == id || gs.Blue.Goalkeeper == id {
		return true
	}
	for _, v := range gs.Waiting.Snapshot() {
		if v == id {
			return true
		}
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func newGameIDLocked() string {
	// Assumes gamesMu is already held when called.
	for {
		b := make([]byte, 12)
		if _, err := crand.Read(b); err != nil {
			// Fallback to timestamp-based entropy if crypto fails (unlikely)
			t := time.Now().UnixNano()
			for i := 0; i < len(b); i++ {
				b[i] = byte(t >> (8 * (i % 8)))
			}
		}
		id := hex.EncodeToString(b)
		if _, exists := games[id]; !exists {
			return id
		}
	}
}
