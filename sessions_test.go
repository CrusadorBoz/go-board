package main

// Tests for sessions.go (identical in all five game apps).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidSessionID(t *testing.T) {
	good := "0123456789abcdef0123456789abcdef"
	if !validSessionID(good) {
		t.Fatalf("minted-style ID rejected: %q", good)
	}
	for _, bad := range []string{
		"", "abc", good + "0", good[:31],
		"0123456789ABCDEF0123456789abcdef", // upper case is never minted
		"0123456789abcdef0123456789abcdeg",
		"0123456789abcdef0123456789abcd\x00f",
	} {
		if validSessionID(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A minted cookie must always pass validSessionID, or every returning player
// would lose their game.
func TestMintedIDIsValid(t *testing.T) {
	rec := httptest.NewRecorder()
	id := sessionID(rec, httptest.NewRequest("GET", "/api/state", nil))
	if !validSessionID(id) {
		t.Fatalf("sessionID minted an ID validSessionID rejects: %q", id)
	}
}

func TestForgedCookieGetsFreshID(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "attacker-chosen-key"})
	rec := httptest.NewRecorder()
	id := sessionID(rec, req)
	if id == "attacker-chosen-key" || !validSessionID(id) {
		t.Fatalf("forged cookie used as key: %q", id)
	}
	if len(rec.Result().Cookies()) != 1 {
		t.Fatal("no replacement cookie was set")
	}

	good := "00112233445566778899aabbccddeeff"
	req = httptest.NewRequest("GET", "/api/state", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: good})
	rec = httptest.NewRecorder()
	if id := sessionID(rec, req); id != good {
		t.Fatalf("valid cookie not reused: got %q", id)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("a valid cookie should not be replaced")
	}
}

func TestStoreIsCapped(t *testing.T) {
	s := newStore()
	s.mu.Lock()
	for i := 0; i < maxGames*3; i++ {
		s.putLocked(fmt.Sprintf("%032x", i), nil)
	}
	n := len(s.games)
	s.mu.Unlock()
	if n > maxGames {
		t.Fatalf("store holds %d games, cap is %d", n, maxGames)
	}
}

// When the store is full, the least recently used games go first: a game
// touched recently survives a flood of new sessions.
func TestEvictionKeepsRecentlyUsed(t *testing.T) {
	s := newStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	player := "ffffffffffffffffffffffffffffffff"
	s.putLocked(player, &Game{})
	for i := 0; i < maxGames-1; i++ {
		s.putLocked(fmt.Sprintf("%032x", i), nil)
	}
	s.getLocked(player) // the player makes a move: most recently used now
	for i := maxGames; i < maxGames+maxGames/2; i++ {
		s.putLocked(fmt.Sprintf("%032x", i), nil)
	}
	if s.games[player] == nil {
		t.Fatal("recently used game was evicted")
	}
	if _, ok := s.games[fmt.Sprintf("%032x", 0)]; ok {
		t.Fatal("least recently used game survived")
	}
}

func TestIdleGamesAreSwept(t *testing.T) {
	s := newStore()
	s.mu.Lock()
	s.putLocked("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &Game{})
	s.putLocked("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", &Game{})
	s.seen["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"] = time.Now().Add(-gameIdleTTL - time.Minute)
	s.mu.Unlock()
	s.sweepIdle()
	if _, ok := s.games["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]; ok {
		t.Fatal("idle game not swept")
	}
	if _, ok := s.games["bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"]; !ok {
		t.Fatal("active game swept")
	}
	if len(s.seen) != len(s.games) {
		t.Fatalf("seen (%d) and games (%d) out of step", len(s.seen), len(s.games))
	}
}

// The attack itself: many cookieless requests must not grow the store past
// the cap.
func TestCookielessFloodIsBounded(t *testing.T) {
	s := newStore()
	for i := 0; i < maxGames+500; i++ {
		rec := httptest.NewRecorder()
		s.handleState(rec, httptest.NewRequest("GET", "/api/state", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i, rec.Code)
		}
	}
	s.mu.Lock()
	n := len(s.games)
	s.mu.Unlock()
	if n > maxGames {
		t.Fatalf("store grew to %d games, cap is %d", n, maxGames)
	}
}
