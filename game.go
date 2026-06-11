// Rules engine for the board game Go (Weiqi / Baduk).
//
// Pure Go, no external deps. Implements stone placement, group/liberty
// detection, captures, the suicide rule, simple ko, passing, and Chinese
// (area) scoring with komi. All game state lives in memory; one *Game per
// browser session (see store in main.go).

package main

// Stone colours. 0 = empty point.
const (
	empty = 0
	black = 1
	white = 2
)

const komi = 6.5 // points added to White to offset Black's first-move advantage

// Game is a single match. The board is a flat slice indexed y*Size + x.
type Game struct {
	Size       int
	Board      []int
	Turn       int  // black or white — whose move it is
	Over       bool // true once both players have passed in a row
	PassCount  int  // consecutive passes
	CapByBlack int  // white stones captured by Black
	CapByWhite int  // black stones captured by White
	Ko         int  // index forbidden to the current player by the ko rule, or -1
	Last       int  // index of the last stone played, or -1 (e.g. after a pass)

	// Opponent configuration. Mode is "human" (two people share the screen),
	// "computer" (human plays Black, the computer plays White), or "auto" (the
	// computer plays both sides). Level ("easy"/"medium"/"hard") applies to
	// every computer-controlled side. Black always moves first.
	Mode  string
	Level string
	Note  string // transient note for the UI, e.g. "the computer passed"

	// Undo/redo history. hist holds a snapshot of every completed position;
	// cur is the index of the position currently on the board.
	hist []snapshot
	cur  int
}

// newGame creates the default match: human (Black) versus a medium computer.
func newGame(size int) *Game {
	return newGameOpts(size, "computer", "medium")
}

// newGameOpts creates a match with an explicit opponent mode and difficulty.
func newGameOpts(size int, mode, level string) *Game {
	if mode != "human" && mode != "computer" && mode != "auto" {
		mode = "computer"
	}
	if level != "easy" && level != "medium" && level != "hard" {
		level = "medium"
	}
	g := &Game{
		Size:  size,
		Board: make([]int, size*size),
		Turn:  black, // Black always moves first
		Ko:    -1,
		Last:  -1,
		Mode:  mode,
		Level: level,
	}
	g.hist = []snapshot{g.takeSnapshot()} // record the empty starting position
	return g
}

// aiControls reports whether the given colour is played by the computer.
func (g *Game) aiControls(color int) bool {
	switch g.Mode {
	case "computer":
		return color == white
	case "auto":
		return true
	default: // human
		return false
	}
}

// --- Undo / redo history ----------------------------------------------------
//
// After each completed action (a human move plus the computer's reply, or a
// pass) the resulting position is pushed onto hist. The player can step back
// through earlier positions and forward again up to the latest real one;
// playing a fresh move from a rewound position discards the forward tail.

type snapshot struct {
	board      []int
	turn       int
	over       bool
	passCount  int
	capByBlack int
	capByWhite int
	ko         int
	last       int
}

func (g *Game) takeSnapshot() snapshot {
	b := make([]int, len(g.Board))
	copy(b, g.Board)
	return snapshot{
		board:      b,
		turn:       g.Turn,
		over:       g.Over,
		passCount:  g.PassCount,
		capByBlack: g.CapByBlack,
		capByWhite: g.CapByWhite,
		ko:         g.Ko,
		last:       g.Last,
	}
}

func (g *Game) restore(s snapshot) {
	g.Board = make([]int, len(s.board))
	copy(g.Board, s.board)
	g.Turn = s.turn
	g.Over = s.over
	g.PassCount = s.passCount
	g.CapByBlack = s.capByBlack
	g.CapByWhite = s.capByWhite
	g.Ko = s.ko
	g.Last = s.last
}

// pushHistory records the current position as a new step, discarding any
// forward (redo) tail left from an earlier rewind.
func (g *Game) pushHistory() {
	g.hist = append(g.hist[:g.cur+1], g.takeSnapshot())
	g.cur = len(g.hist) - 1
}

func (g *Game) canUndo() bool { return g.cur > 0 }
func (g *Game) canRedo() bool { return g.cur < len(g.hist)-1 }

// undo / redo step the board one recorded position back or forward and report
// whether they moved.
func (g *Game) undo() bool {
	if !g.canUndo() {
		return false
	}
	g.cur--
	g.restore(g.hist[g.cur])
	return true
}

func (g *Game) redo() bool {
	if !g.canRedo() {
		return false
	}
	g.cur++
	g.restore(g.hist[g.cur])
	return true
}

// clone returns a deep copy, used by the AI to evaluate hypothetical moves.
func (g *Game) clone() *Game {
	c := *g
	c.Board = make([]int, len(g.Board))
	copy(c.Board, g.Board)
	return &c
}

func (g *Game) idx(x, y int) int       { return y*g.Size + x }
func (g *Game) inBounds(x, y int) bool { return x >= 0 && x < g.Size && y >= 0 && y < g.Size }

// neighbors returns the orthogonally adjacent indices of i.
func (g *Game) neighbors(i int) []int {
	x, y := i%g.Size, i/g.Size
	n := make([]int, 0, 4)
	if x > 0 {
		n = append(n, i-1)
	}
	if x < g.Size-1 {
		n = append(n, i+1)
	}
	if y > 0 {
		n = append(n, i-g.Size)
	}
	if y < g.Size-1 {
		n = append(n, i+g.Size)
	}
	return n
}

// group flood-fills the connected same-colour group containing i and reports
// how many distinct empty points (liberties) it touches.
func (g *Game) group(i int) (stones []int, liberties int) {
	color := g.Board[i]
	seen := map[int]bool{i: true}
	libs := map[int]bool{}
	stack := []int{i}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		stones = append(stones, c)
		for _, nb := range g.neighbors(c) {
			switch {
			case g.Board[nb] == empty:
				libs[nb] = true
			case g.Board[nb] == color && !seen[nb]:
				seen[nb] = true
				stack = append(stack, nb)
			}
		}
	}
	return stones, len(libs)
}

// play attempts a move at (x, y) for the player to move. It returns "" on a
// legal move (mutating the game) or a human-readable reason if the move is
// rejected (leaving the game unchanged).
func (g *Game) play(x, y int) string {
	if g.Over {
		return "The game is over — start a new one."
	}
	if !g.inBounds(x, y) {
		return "That point is off the board."
	}
	i := g.idx(x, y)
	if g.Board[i] != empty {
		return "That point is already occupied."
	}
	if i == g.Ko {
		return "Ko: you can't recapture there yet — play elsewhere first."
	}

	me := g.Turn
	opp := black + white - me // the other colour
	g.Board[i] = me

	// Remove any adjacent enemy groups left without liberties.
	dead := map[int]bool{}
	for _, nb := range g.neighbors(i) {
		if g.Board[nb] == opp {
			if stones, libs := g.group(nb); libs == 0 {
				for _, s := range stones {
					dead[s] = true
				}
			}
		}
	}
	for s := range dead {
		g.Board[s] = empty
	}
	if n := len(dead); n > 0 {
		if me == black {
			g.CapByBlack += n
		} else {
			g.CapByWhite += n
		}
	}

	// Suicide rule: if our own stone's group still has no liberties and we
	// captured nothing, the move is illegal — revert it.
	if _, libs := g.group(i); libs == 0 {
		g.Board[i] = empty
		return "That would be suicide — not allowed."
	}

	// Simple ko: capturing exactly one stone with a lone stone that now has a
	// single liberty forbids an immediate recapture at the captured point.
	g.Ko = -1
	if len(dead) == 1 {
		if stones, libs := g.group(i); len(stones) == 1 && libs == 1 {
			for s := range dead {
				g.Ko = s
			}
		}
	}

	g.Last = i
	g.PassCount = 0
	g.Turn = opp
	return ""
}

// pass forfeits the turn. Two passes in a row end the game.
func (g *Game) pass() {
	if g.Over {
		return
	}
	g.Ko = -1
	g.Last = -1
	g.PassCount++
	g.Turn = black + white - g.Turn
	if g.PassCount >= 2 {
		g.Over = true
	}
}

// Score is the result of area (Chinese) scoring.
type Score struct {
	Black  float64 `json:"black"`
	White  float64 `json:"white"`
	Winner string  `json:"winner"`
}

// score computes area scoring: each player's stones on the board plus the
// empty regions surrounded solely by that player's colour, with komi to White.
func (g *Game) score() Score {
	var blackStones, whiteStones, blackTerr, whiteTerr int
	for _, c := range g.Board {
		switch c {
		case black:
			blackStones++
		case white:
			whiteStones++
		}
	}

	visited := make([]bool, len(g.Board))
	for i := range g.Board {
		if g.Board[i] != empty || visited[i] {
			continue
		}
		// Flood the empty region and note which colours border it.
		region := 0
		var touchesBlack, touchesWhite bool
		stack := []int{i}
		visited[i] = true
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			region++
			for _, nb := range g.neighbors(c) {
				switch g.Board[nb] {
				case empty:
					if !visited[nb] {
						visited[nb] = true
						stack = append(stack, nb)
					}
				case black:
					touchesBlack = true
				case white:
					touchesWhite = true
				}
			}
		}
		switch {
		case touchesBlack && !touchesWhite:
			blackTerr += region
		case touchesWhite && !touchesBlack:
			whiteTerr += region
		}
	}

	b := float64(blackStones + blackTerr)
	w := float64(whiteStones+whiteTerr) + komi
	winner := "Black"
	switch {
	case w > b:
		winner = "White"
	case w == b:
		winner = "Tie"
	}
	return Score{Black: b, White: w, Winner: winner}
}
