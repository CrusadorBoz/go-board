# go-board

A web-based **Go** board game (Weiqi / Baduk) written in Go. It serves a small
HTML/CSS/JS front-end and a JSON API backed by a rules engine — captures, ko,
suicide prevention, scoring, undo/redo.

Live at <https://bozcode.com/goapp/go-board/>.

## Play modes

- **Vs. computer** — play against a built-in AI (`ai.go`)
- **Two player** — local hotseat
- **Watch** — the AI plays itself

## Run locally

```sh
go build -o go-board .
./go-board
# serves on 127.0.0.1:8085
```

Override the bind address with `GOBOARD_ADDR`, e.g.:

```sh
GOBOARD_ADDR=127.0.0.1:9000 ./go-board
```

Then open <http://127.0.0.1:8085/> in a browser.

## Layout

| File / dir | Purpose |
|------------|---------|
| `main.go`  | HTTP server, session handling, JSON API |
| `game.go`  | Rules engine (board state, captures, ko, scoring) |
| `ai.go`    | Computer opponent |
| `assets/`  | Front-end (`index.html`, `styles.css`, `app.js`, `icon.svg`), embedded via `embed` |

## Deployment

The binary is self-contained (assets are embedded), so deployment is just:
build it, run it behind a reverse proxy, and point the proxy at its address
(default `127.0.0.1:8085`, overridable via `GOBOARD_ADDR`).
