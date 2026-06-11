// Go web server for the board game Go (Weiqi / Baduk).
//
// Serves a small HTML/CSS/JS front-end (the board) and a JSON API that drives
// the rules engine in game.go. Each browser session gets its own match, kept
// in memory and keyed by the goban_id cookie.
//
// Designed to sit behind the bozcode.com nginx reverse proxy at
// /goapp/go-board/, gated to any signed-in user via the Django auth_request
// endpoint /_goapp-auth-user/. nginx strips the /goapp/go-board prefix, so this
// server sees plain paths like "/" and "/api/move".

package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
)

const (
	defaultAddr = "127.0.0.1:8085"
	cookieName  = "goban_id"
	cookiePath  = "/goapp/go-board/" // public path the browser sees, not the proxied one
)

// listenAddr is the default bind address, overridable via GOBOARD_ADDR (handy
// for testing a fresh build on a spare port while the live service runs).
func listenAddr() string {
	if a := os.Getenv("GOBOARD_ADDR"); a != "" {
		return a
	}
	return defaultAddr
}

//go:embed assets/index.html assets/styles.css assets/app.js assets/icon.svg
var assets embed.FS

func main() {
	st := newStore()
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/styles.css", serveAsset("assets/styles.css", "text/css; charset=utf-8"))
	mux.HandleFunc("/app.js", serveAsset("assets/app.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("/icon.svg", serveAsset("assets/icon.svg", "image/svg+xml"))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("/api/state", st.handleState)
	mux.HandleFunc("/api/move", st.handleMove)
	mux.HandleFunc("/api/pass", st.handlePass)
	mux.HandleFunc("/api/new", st.handleNew)
	mux.HandleFunc("/api/undo", st.handleUndo)
	mux.HandleFunc("/api/redo", st.handleRedo)
	mux.HandleFunc("/api/step", st.handleStep)

	addr := listenAddr()
	log.Printf("go-board listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, logRequests(mux)))
}

// ---------------------------------------------------------------------------
// Session store: one match per browser, guarded by a single mutex.
// ---------------------------------------------------------------------------

type store struct {
	mu    sync.Mutex
	games map[string]*Game
}

func newStore() *store { return &store{games: map[string]*Game{}} }

// with runs fn against the caller's game under lock, creating a default 9×9
// match the first time a session is seen.
func (s *store) with(id string, fn func(g *Game)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.games[id]
	if g == nil {
		g = newGame(9)
		s.games[id] = g
	}
	fn(g)
}

func (s *store) reset(id string, g *Game) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.games[id] = g
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func (s *store) handleState(w http.ResponseWriter, r *http.Request) {
	id := sessionID(w, r)
	var out stateDTO
	s.with(id, func(g *Game) { out = dto(g, "") })
	writeJSON(w, out)
}

func (s *store) handleMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct{ X, Y int }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id := sessionID(w, r)
	var out stateDTO
	s.with(id, func(g *Game) {
		g.Note = ""
		if g.aiControls(g.Turn) { // not a human's turn to place a stone
			out = dto(g, "")
			return
		}
		msg := g.play(body.X, body.Y)
		if msg == "" {
			g.aiRespond()  // computer replies in the same request, if enabled
			g.pushHistory() // record the completed position for undo/redo
		}
		out = dto(g, msg)
	})
	writeJSON(w, out)
}

func (s *store) handlePass(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	id := sessionID(w, r)
	var out stateDTO
	s.with(id, func(g *Game) {
		g.Note = ""
		if !g.Over && !g.aiControls(g.Turn) { // only a human passes manually
			g.pass()
			if !g.Over {
				g.aiRespond() // let the computer answer the pass
			}
			g.pushHistory()
		}
		out = dto(g, "")
	})
	writeJSON(w, out)
}

// handleStep advances a computer-vs-computer game by one move. The front-end
// calls it on a timer to play the match out at a watchable pace.
func (s *store) handleStep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	id := sessionID(w, r)
	var out stateDTO
	s.with(id, func(g *Game) {
		g.Note = ""
		if g.aiStep() {
			g.pushHistory()
		}
		out = dto(g, "")
	})
	writeJSON(w, out)
}

func (s *store) handleUndo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	id := sessionID(w, r)
	var out stateDTO
	s.with(id, func(g *Game) {
		g.Note = ""
		if g.undo() {
			g.Note = "Rewound — step forward, or play a different move."
		}
		out = dto(g, "")
	})
	writeJSON(w, out)
}

func (s *store) handleRedo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	id := sessionID(w, r)
	var out stateDTO
	s.with(id, func(g *Game) {
		g.Note = ""
		if g.redo() {
			g.Note = "Stepped forward."
		}
		out = dto(g, "")
	})
	writeJSON(w, out)
}

func (s *store) handleNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Size     int    `json:"size"`
		Opponent string `json:"opponent"` // "computer" (default), "human", or "auto"
		Level    string `json:"level"`    // easy / medium / hard
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	size := body.Size
	if size != 9 && size != 13 && size != 19 {
		size = 9
	}
	mode := "computer" // default to playing the computer
	switch body.Opponent {
	case "human":
		mode = "human"
	case "auto":
		mode = "auto"
	}
	id := sessionID(w, r)
	s.reset(id, newGameOpts(size, mode, body.Level))
	var out stateDTO
	s.with(id, func(g *Game) { out = dto(g, "") })
	writeJSON(w, out)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	serveAsset("assets/index.html", "text/html; charset=utf-8")(w, r)
}

// ---------------------------------------------------------------------------
// Serialization + helpers
// ---------------------------------------------------------------------------

// stateDTO is the JSON shape the front-end consumes.
type stateDTO struct {
	Size       int    `json:"size"`
	Board      []int  `json:"board"`
	Turn       int    `json:"turn"`
	Over       bool   `json:"over"`
	PassCount  int    `json:"passCount"`
	CapByBlack int    `json:"capByBlack"`
	CapByWhite int    `json:"capByWhite"`
	Ko         int    `json:"ko"`
	Last       int    `json:"last"`
	Mode       string `json:"mode"`
	Level      string `json:"level"`
	Note       string `json:"note,omitempty"`
	CanUndo    bool   `json:"canUndo"`
	CanRedo    bool   `json:"canRedo"`
	Message    string `json:"message,omitempty"`
	Score      *Score `json:"score,omitempty"`
}

func dto(g *Game, msg string) stateDTO {
	d := stateDTO{
		Size:       g.Size,
		Board:      g.Board,
		Turn:       g.Turn,
		Over:       g.Over,
		PassCount:  g.PassCount,
		CapByBlack: g.CapByBlack,
		CapByWhite: g.CapByWhite,
		Ko:         g.Ko,
		Last:       g.Last,
		Mode:       g.Mode,
		Level:      g.Level,
		Note:       g.Note,
		CanUndo:    g.canUndo(),
		CanRedo:    g.canRedo(),
		Message:    msg,
	}
	if g.Over {
		sc := g.score()
		d.Score = &sc
	}
	return d
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func serveAsset(key, mime string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := assets.ReadFile(key)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	}
}

// sessionID reads the session cookie or mints a new one (set on the response).
func sessionID(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    id,
		Path:     cookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
	return id
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
