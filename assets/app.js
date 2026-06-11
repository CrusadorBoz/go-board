// Front-end for the Go board game. Renders the board on a <canvas> and drives
// the Go server's JSON API. All paths are relative so they resolve under
// /goapp/go-board/ behind the nginx proxy.

(function () {
  "use strict";

  var BLACK = 1, WHITE = 2;
  var LOGICAL = 540;          // logical canvas size in px (scaled for DPR)
  var MARGIN = 28;            // board margin to first line

  var canvas = document.getElementById("board");
  var ctx = canvas.getContext("2d");
  var statusEl = document.getElementById("status");
  var capsEl = document.getElementById("caps");
  var bylineEl = document.getElementById("byline");
  var sizeSel = document.getElementById("size");
  var opponentSel = document.getElementById("opponent");
  var levelSel = document.getElementById("level");
  var passBtn = document.getElementById("pass");
  var playPauseBtn = document.getElementById("playpause");
  var stepBtn = document.getElementById("step");

  var state = null;           // latest server state
  var gap = 0;                // px between lines (depends on board size)
  var paused = false;         // computer-vs-computer playback paused?
  var autoTimer = null;       // pending auto-step timer, if any
  var AUTO_DELAY = 700;       // ms between moves when watching the computers

  // Hoshi (star point) coordinates per board size.
  var STARS = {
    9:  [[2, 2], [6, 2], [4, 4], [2, 6], [6, 6]],
    13: [[3, 3], [9, 3], [6, 6], [3, 9], [9, 9]],
    19: [[3, 3], [9, 3], [15, 3], [3, 9], [9, 9], [15, 9], [3, 15], [9, 15], [15, 15]]
  };

  function setupCanvas() {
    var dpr = window.devicePixelRatio || 1;
    canvas.width = LOGICAL * dpr;
    canvas.height = LOGICAL * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  }

  function pointPx(coord) {
    return MARGIN + coord * gap;
  }

  function draw() {
    if (!state) return;
    var n = state.size;
    gap = (LOGICAL - 2 * MARGIN) / (n - 1);

    ctx.clearRect(0, 0, LOGICAL, LOGICAL);

    // Grid lines.
    ctx.strokeStyle = "#4a3211";
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (var k = 0; k < n; k++) {
      var p = pointPx(k);
      ctx.moveTo(pointPx(0), p); ctx.lineTo(pointPx(n - 1), p); // horizontal
      ctx.moveTo(p, pointPx(0)); ctx.lineTo(p, pointPx(n - 1)); // vertical
    }
    ctx.stroke();

    // Star points.
    var stars = STARS[n] || [];
    ctx.fillStyle = "#2c1d08";
    for (var s = 0; s < stars.length; s++) {
      ctx.beginPath();
      ctx.arc(pointPx(stars[s][0]), pointPx(stars[s][1]), 3, 0, 2 * Math.PI);
      ctx.fill();
    }

    // Stones.
    var r = gap * 0.46;
    for (var i = 0; i < state.board.length; i++) {
      var c = state.board[i];
      if (c === 0) continue;
      var x = pointPx(i % n), y = pointPx(Math.floor(i / n));
      drawStone(x, y, r, c, i === state.last);
    }

    // Ko marker.
    if (state.ko >= 0) {
      var kx = pointPx(state.ko % n), ky = pointPx(Math.floor(state.ko / n));
      ctx.strokeStyle = "#b00020";
      ctx.lineWidth = 2;
      ctx.strokeRect(kx - r * 0.5, ky - r * 0.5, r, r);
    }
  }

  function drawStone(x, y, r, color, isLast) {
    var grad = ctx.createRadialGradient(x - r * 0.35, y - r * 0.35, r * 0.1, x, y, r);
    if (color === BLACK) {
      grad.addColorStop(0, "#6b6b6b");
      grad.addColorStop(1, "#070707");
    } else {
      grad.addColorStop(0, "#ffffff");
      grad.addColorStop(1, "#c9c9c9");
    }
    ctx.fillStyle = grad;
    ctx.beginPath();
    ctx.arc(x, y, r, 0, 2 * Math.PI);
    ctx.fill();

    if (isLast) {
      ctx.strokeStyle = color === BLACK ? "#ffffff" : "#1c1407";
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.arc(x, y, r * 0.38, 0, 2 * Math.PI);
      ctx.stroke();
    }
  }

  function render() {
    draw();
    if (!state) return;

    if (state.over && state.score) {
      var sc = state.score;
      statusEl.innerHTML =
        '<span class="win">Game over — ' + sc.winner + " wins. " +
        "Black " + sc.black + " · White " + sc.white + "</span>";
    } else {
      var parts = [];
      if (state.message) {
        parts.push('<span class="warn">' + escapeHtml(state.message) + "</span>");
      } else {
        var who = state.turn === BLACK ? "Black" : "White";
        var extra = (state.mode === "human" && state.passCount === 1) ? " (opponent passed)" : "";
        parts.push(who + " to move" + extra);
      }
      if (state.note) parts.push('<span class="note">' + escapeHtml(state.note) + "</span>");
      statusEl.innerHTML = parts.join(" · ");
    }

    capsEl.innerHTML =
      "Captured by Black: <b>" + state.capByBlack + "</b>" +
      "Captured by White: <b>" + state.capByWhite + "</b>";
  }

  function escapeHtml(s) {
    return s.replace(/[&<>"]/g, function (ch) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[ch];
    });
  }

  // --- API calls -----------------------------------------------------------

  function api(path, body) {
    var opts = { method: body ? "POST" : "GET", headers: {} };
    if (body) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (r) {
      if (!r.ok) throw new Error("request failed: " + r.status);
      return r.json();
    });
  }

  function load() {
    api("api/state").then(apply).catch(showError);
  }

  function apply(s) {
    state = s;
    syncControls();
    updateByline();
    render();
    scheduleAuto(); // keep a computer-vs-computer game moving
  }

  function syncControls() {
    if (!state) return;
    var mode = state.mode || "computer";
    sizeSel.value = String(state.size);
    opponentSel.value = mode;
    if (mode !== "human" && state.level) levelSel.value = state.level;
    levelSel.disabled = mode === "human"; // both computer modes use the level
    document.getElementById("undo").disabled = !state.canUndo;
    document.getElementById("redo").disabled = !state.canRedo;

    // In watch (auto) mode the human doesn't place stones or pass; instead they
    // get playback controls.
    var auto = mode === "auto";
    passBtn.hidden = auto;
    playPauseBtn.hidden = !auto;
    stepBtn.hidden = !auto;
    playPauseBtn.textContent = paused ? "Resume" : "Pause";
    playPauseBtn.disabled = state.over;
    stepBtn.disabled = state.over || !paused; // step only while paused
  }

  function titleCase(s) { return s ? s.charAt(0).toUpperCase() + s.slice(1) : ""; }

  function updateByline() {
    if (!state) return;
    var lvl = titleCase(state.level);
    if (state.mode === "auto") {
      bylineEl.textContent =
        "Computer vs computer (" + lvl + ") ● ○ — sit back and watch.";
    } else if (state.mode === "computer") {
      bylineEl.textContent =
        "You play Black ● — the computer (" + lvl + ") plays White ○.";
    } else {
      bylineEl.textContent = "Two players, same screen. Black moves first.";
    }
  }

  // --- Computer-vs-computer playback ---------------------------------------

  function stopAuto() {
    if (autoTimer) { clearTimeout(autoTimer); autoTimer = null; }
  }

  // scheduleAuto queues the next self-play move, unless the game is paused,
  // over, or not in watch mode. apply() calls this after every state update, so
  // each applied step naturally schedules the one after it.
  function scheduleAuto() {
    stopAuto();
    if (!state || state.mode !== "auto" || state.over || paused) return;
    autoTimer = setTimeout(function () {
      api("api/step", {}).then(apply).catch(showError);
    }, AUTO_DELAY);
  }

  function showError() {
    statusEl.innerHTML = '<span class="warn">Could not reach the game server.</span>';
  }

  // --- Input ---------------------------------------------------------------

  canvas.addEventListener("click", function (ev) {
    if (!state || state.over) return;
    var rect = canvas.getBoundingClientRect();
    var scale = LOGICAL / rect.width; // CSS px -> logical px
    var lx = (ev.clientX - rect.left) * scale;
    var ly = (ev.clientY - rect.top) * scale;
    var gx = Math.round((lx - MARGIN) / gap);
    var gy = Math.round((ly - MARGIN) / gap);
    if (gx < 0 || gy < 0 || gx >= state.size || gy >= state.size) return;
    // Reject clicks that landed too far from an intersection.
    if (Math.abs(pointPx(gx) - lx) > gap * 0.5 || Math.abs(pointPx(gy) - ly) > gap * 0.5) return;
    api("api/move", { x: gx, y: gy }).then(apply).catch(showError);
  });

  document.getElementById("pass").addEventListener("click", function () {
    api("api/pass", {}).then(apply).catch(showError);
  });

  // Stepping back/forward pauses self-play so the game doesn't run away while
  // you review it.
  document.getElementById("undo").addEventListener("click", function () {
    paused = true;
    api("api/undo", {}).then(apply).catch(showError);
  });

  document.getElementById("redo").addEventListener("click", function () {
    paused = true;
    api("api/redo", {}).then(apply).catch(showError);
  });

  // Watch-mode playback: pause/resume the computers, or step one move.
  playPauseBtn.addEventListener("click", function () {
    paused = !paused;
    syncControls();
    scheduleAuto(); // resumes if just unpaused; no-op if paused
  });

  stepBtn.addEventListener("click", function () {
    api("api/step", {}).then(apply).catch(showError);
  });

  document.getElementById("new").addEventListener("click", function () {
    paused = false; // a fresh watch game starts playing immediately
    api("api/new", {
      size: parseInt(sizeSel.value, 10),
      opponent: opponentSel.value,
      level: levelSel.value
    }).then(apply).catch(showError);
  });

  // The level applies to both computer modes, but not to two human players.
  opponentSel.addEventListener("change", function () {
    levelSel.disabled = opponentSel.value === "human";
  });

  // Hamburger dropdown menus: only one open at a time; click outside closes.
  var menus = Array.prototype.slice.call(document.querySelectorAll(".menu"));
  menus.forEach(function (m) {
    m.addEventListener("toggle", function () {
      if (!m.open) return;
      menus.forEach(function (o) { if (o !== m) o.open = false; });
    });
  });
  document.addEventListener("click", function (e) {
    menus.forEach(function (m) {
      if (m.open && !m.contains(e.target)) m.open = false;
    });
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") menus.forEach(function (m) { m.open = false; });
  });

  setupCanvas();
  load();
})();
