package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/abrardw/lps/internal/docker"
)

// Terminal protocol (one WebSocket per session):
//   - binary frames, both directions: raw terminal bytes
//   - text frames, client -> server: JSON control messages, e.g.
//     {"type":"resize","cols":120,"rows":32}
type termControl struct {
	Type string `json:"type"`
	Cols uint   `json:"cols"`
	Rows uint   `json:"rows"`
}

// sameOrigin rejects cross-site WebSocket handshakes. Browsers always send
// Origin on WebSocket requests, and unlike fetch there is no CORS preflight, so
// this check (on top of the SameSite=Strict session cookie) is what stops a
// malicious page from opening a shell. Behind `tailscale serve` the browser's
// host arrives as X-Forwarded-Host, which is only trusted from loopback.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser; it can't carry the user's cookie cross-site
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if viaLocalProxy(r) {
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" && strings.EqualFold(u.Host, fh) {
			return true
		}
	}
	return false
}

func termSize(r *http.Request, key string, def uint) uint {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || n < 2 || n > 1000 {
		return def
	}
	return uint(n)
}

// handleVPSTerminal opens an interactive shell in the VPS over a WebSocket.
func (s *Server) handleVPSTerminal(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadVPS(w, r)
	if !ok {
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin terminal request refused")
		return
	}
	st, err := s.engine.State(r.Context(), v.ContainerID)
	if err != nil || st != docker.StateRunning {
		writeError(w, http.StatusConflict, "the VPS is not running; start it first")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // origin already checked by sameOrigin (incl. tailscale serve)
	})
	if err != nil {
		return // Accept has written the HTTP error
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	// Not r.Context(): the session must end on server shutdown too.
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	term, err := s.engine.OpenTerminal(ctx, v.ContainerID, v.SSHUser, termSize(r, "cols", 80), termSize(r, "rows", 24))
	if err != nil {
		msg := err.Error()
		if len(msg) > 120 { // close reasons are limited to 123 bytes
			msg = msg[:120]
		}
		conn.Close(websocket.StatusInternalError, msg)
		return
	}
	defer term.Close()
	s.log.Info("terminal opened", "vps", v.Name, "user", v.SSHUser, "ip", clientIP(r))
	s.info(r, "vps.terminal", subjVPS, v.ID, v.Name, "Terminal opened on %s", v.Name)
	defer s.log.Info("terminal closed", "vps", v.Name)

	// Shell output -> browser.
	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				conn.Close(websocket.StatusNormalClosure, "shell exited")
				return
			}
		}
	}()

	// Keepalive so proxies (e.g. tailscale serve) don't drop idle sessions.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Browser -> shell input and control messages.
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageBinary:
			if _, err := term.Write(data); err != nil {
				return
			}
		case websocket.MessageText:
			var c termControl
			if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Cols > 0 && c.Rows > 0 && c.Cols <= 1000 && c.Rows <= 1000 {
				_ = term.Resize(ctx, c.Cols, c.Rows)
			}
		}
	}
}
