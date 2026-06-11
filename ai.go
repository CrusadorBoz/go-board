// Heuristic computer opponent for the Go board game.
//
// No search tree — each candidate move is scored by a handful of Go-sensible
// heuristics (capture, save groups in atari, atari the enemy, avoid self-atari,
// don't fill your own eyes, prefer the centre). Difficulty changes how much
// random noise is mixed in: more noise = looser, weaker play.

package main

import (
	"math"
	"math/rand"
)

// aiRespond plays the computer's reply after a human move. It runs only in
// human-vs-computer games (exactly once per human move); two-player and
// computer-vs-computer games never use it. Self-play is advanced one move at a
// time via aiStep so it can be watched.
func (g *Game) aiRespond() {
	for !g.Over && g.Mode == "computer" && g.aiControls(g.Turn) {
		g.aiMove()
	}
}

// aiStep plays exactly one computer move (or pass) if it is a computer side's
// turn. Used to advance computer-vs-computer games one ply at a time. Returns
// true if it moved.
func (g *Game) aiStep() bool {
	if g.Over || !g.aiControls(g.Turn) {
		return false
	}
	g.aiMove()
	return true
}

// aiMove picks and plays the computer's best move, or passes if every legal
// move would only hurt its position.
func (g *Game) aiMove() {
	me := g.Turn
	bestScore := math.Inf(-1)
	bestMove := -1

	for i := range g.Board {
		if g.Board[i] != empty || i == g.Ko {
			continue
		}
		score, legal := g.evalMove(i, me)
		if !legal {
			continue
		}
		score += rand.Float64() * g.aiJitter() // difficulty noise
		if score > bestScore {
			bestScore = score
			bestMove = i
		}
	}

	if bestMove < 0 || bestScore < g.aiPassFloor() {
		g.pass()
		g.Note = "The computer passed — pass as well to end and score the game, or keep playing."
		return
	}
	g.Note = ""
	g.play(bestMove%g.Size, bestMove/g.Size)
}

// evalMove scores playing at index i for player me. The bool is false when the
// move is illegal (suicide, occupied, ko) and should be skipped.
func (g *Game) evalMove(i, me int) (float64, bool) {
	x, y := i%g.Size, i/g.Size

	// Legality + resulting position via a throwaway clone.
	c := g.clone()
	if msg := c.play(x, y); msg != "" {
		return 0, false
	}

	opp := black + white - me
	var captured int
	if me == black {
		captured = c.CapByBlack - g.CapByBlack
	} else {
		captured = c.CapByWhite - g.CapByWhite
	}

	score := 0.0

	// Capturing enemy stones is the strongest single signal.
	score += float64(captured) * 12.0

	// Liberties of our own stone after the move.
	_, myLibs := c.group(i)
	if captured == 0 && myLibs == 1 {
		score -= 9.0 // walking into self-atari: avoid
	}
	if myLibs >= 3 {
		score += 0.5 // healthy, well-connected move
	}

	// Putting an adjacent enemy group into atari (one liberty) is good.
	for _, nb := range c.neighbors(i) {
		if c.Board[nb] == opp {
			if _, l := c.group(nb); l == 1 {
				score += 3.0
			}
		}
	}

	// Rescue our own group if it was in atari before this move.
	for _, nb := range g.neighbors(i) {
		if g.Board[nb] == me {
			if _, l := g.group(nb); l == 1 && myLibs > 1 {
				score += 4.0
			}
		}
	}

	// Don't fill our own eye: a point whose every orthogonal neighbour is
	// already ours is almost certainly an eye we want to keep.
	nbs := g.neighbors(i)
	own, occupied := 0, 0
	for _, nb := range nbs {
		if g.Board[nb] == me {
			own++
		}
		if g.Board[nb] != empty {
			occupied++
		}
	}
	if own == len(nbs) {
		score -= 6.0
	}

	// Prefer staying in contact with existing stones rather than scattering.
	if occupied > 0 {
		score += 0.6
	}

	// Mild dislike of the first line (edges), and a small pull toward centre.
	if x == 0 || y == 0 || x == g.Size-1 || y == g.Size-1 {
		score -= 1.0
	}
	mid := float64(g.Size-1) / 2
	dist := math.Abs(float64(x)-mid) + math.Abs(float64(y)-mid)
	score -= dist * 0.05

	return score, true
}

// aiJitter is the amount of random noise added to each move's score. Higher
// noise drowns out the heuristics, producing weaker, more varied play.
func (g *Game) aiJitter() float64 {
	switch g.Level {
	case "easy":
		return 8.0
	case "hard":
		return 0.5
	default: // medium
		return 2.0
	}
}

// aiPassFloor is the score below which the AI would rather pass than play a
// self-damaging move (e.g. only eye-filling or self-atari moves remain).
func (g *Game) aiPassFloor() float64 {
	if g.Level == "easy" {
		return -2.0
	}
	return -3.0
}
