package main

import "testing"

// pushHistory keeps at most maxHistory positions, and undo still works from
// the newest one back to the oldest kept.
func TestHistoryIsCapped(t *testing.T) {
	g := newGame(19)
	for i := 0; i < maxHistory+250; i++ {
		g.pushHistory()
	}
	if len(g.hist) != maxHistory {
		t.Fatalf("history holds %d positions, cap is %d", len(g.hist), maxHistory)
	}
	if g.cur != len(g.hist)-1 {
		t.Fatalf("cur = %d, want the newest position %d", g.cur, len(g.hist)-1)
	}
	undos := 0
	for g.undo() {
		undos++
	}
	if undos != maxHistory-1 {
		t.Fatalf("undid %d steps, want %d", undos, maxHistory-1)
	}
}
