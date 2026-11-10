package main

// Session-store limits (security review 2026-11-08).
//
// The store used to keep every game forever, keyed by whatever cookie value
// the browser sent, and a request without a cookie minted a new game. An
// anonymous client could add ~8 KB of memory per request until the whole
// server ran out. Now:
//   - only the 32-hex-character IDs this server mints are accepted as keys;
//   - at most maxGames games are kept; when full, the least recently used
//     tenth is dropped (players active in the last few minutes survive);
//   - a game nobody has touched for gameIdleTTL is forgotten.
//
// This file is identical in backgammon, euchre, texas-holdem, go-board and
// fantasy-kingdoms; change all five together.

import (
	"sort"
	"time"
)

const (
	maxGames      = 5000
	gameIdleTTL   = 72 * time.Hour
	sweepInterval = 30 * time.Minute
)

// validSessionID reports whether v looks like an ID sessionID minted:
// 16 random bytes, hex encoded (32 lowercase hex characters).
func validSessionID(v string) bool {
	if len(v) != 32 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// getLocked returns the game for id (nil if none) and marks it as used.
// The caller holds s.mu.
func (s *store) getLocked(id string) *Game {
	g := s.games[id]
	if g != nil {
		s.seen[id] = time.Now()
	}
	return g
}

// putLocked stores g under id, making room first when the store is full.
// The caller holds s.mu.
func (s *store) putLocked(id string, g *Game) {
	now := time.Now()
	if _, ok := s.games[id]; !ok && len(s.games) >= maxGames {
		s.evictLocked(now)
	}
	s.games[id] = g
	s.seen[id] = now
}

// evictLocked forgets idle games, then, if the store is still at least 90%
// full, the least recently used games down to 90%. The caller holds s.mu.
func (s *store) evictLocked(now time.Time) {
	for id, t := range s.seen {
		if now.Sub(t) > gameIdleTTL {
			delete(s.games, id)
			delete(s.seen, id)
		}
	}
	keep := maxGames * 9 / 10
	if len(s.games) < keep {
		return
	}
	type idTime struct {
		id string
		t  time.Time
	}
	all := make([]idTime, 0, len(s.seen))
	for id, t := range s.seen {
		all = append(all, idTime{id, t})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t.Before(all[j].t) })
	for _, it := range all[:len(all)-keep] {
		delete(s.games, it.id)
		delete(s.seen, it.id)
	}
}

// sweepIdle forgets games nobody has touched for gameIdleTTL.
func (s *store) sweepIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, t := range s.seen {
		if now.Sub(t) > gameIdleTTL {
			delete(s.games, id)
			delete(s.seen, id)
		}
	}
}

// sweepEvery runs sweepIdle forever; main starts it in a goroutine.
func (s *store) sweepEvery(d time.Duration) {
	for range time.Tick(d) {
		s.sweepIdle()
	}
}
