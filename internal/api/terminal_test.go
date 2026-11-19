package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func runningVPS(t *testing.T, h *harness) vpsView {
	t.Helper()
	var v vpsView
	h.do("POST", "/api/vps", createVPSReq{Name: "box", SSHUser: "dev", Password: "password1"}, &v)
	waitFor(t, func() bool {
		var got vpsView
		h.do("GET", fmt.Sprintf("/api/vps/%d", v.ID), nil, &got)
		return got.State == "running"
	})
	return v
}

func dialTerm(h *harness, id int64, origin string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := strings.Replace(h.ts.URL, "http", "ws", 1) + fmt.Sprintf("/api/vps/%d/terminal?cols=100&rows=30", id)
	hdr := http.Header{}
	if origin != "" {
		hdr.Set("Origin", origin)
	}
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: h.c, HTTPHeader: hdr})
}

func TestTerminalSession(t *testing.T) {
	h := setup(t)
	h.login()
	v := runningVPS(t, h)

	conn, _, err := dialTerm(h, v.ID, h.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(data) != "echo:ls\r" {
		t.Fatalf("got %v %q %v", typ, data, err)
	}
	conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":150,"rows":40}`))
	conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":0,"rows":40}`)) // ignored
	conn.Write(ctx, websocket.MessageBinary, []byte("pwd"))
	conn.Read(ctx) // wait until the resize has been processed (messages are handled in order)

	h.eng.mu.Lock()
	term := h.eng.lastTerm
	h.eng.mu.Unlock()
	term.mu.Lock()
	resizes := fmt.Sprint(term.resizes)
	term.mu.Unlock()
	if term.user != "dev" || resizes != "[[100 30] [150 40]]" {
		t.Fatalf("user=%s resizes=%s", term.user, resizes)
	}

	// The shell exiting closes the WebSocket.
	conn.Write(ctx, websocket.MessageBinary, []byte("exit"))
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("want normal closure when the shell exits, got %v", err)
	}
}

func TestTerminalRefusals(t *testing.T) {
	h := setup(t)
	h.login()
	v := runningVPS(t, h)

	if _, res, err := dialTerm(h, v.ID, "https://evil.example"); err == nil || res == nil || res.StatusCode != 403 {
		t.Fatalf("cross-origin should be 403, got %v %v", res, err)
	}

	h.do("POST", fmt.Sprintf("/api/vps/%d/stop", v.ID), nil, nil)
	if _, res, err := dialTerm(h, v.ID, h.ts.URL); err == nil || res == nil || res.StatusCode != 409 {
		t.Fatalf("stopped VPS should be 409, got %v %v", res, err)
	}

	h.do("POST", "/api/auth/logout", nil, nil)
	if _, res, err := dialTerm(h, v.ID, h.ts.URL); err == nil || res == nil || res.StatusCode != 401 {
		t.Fatalf("logged out should be 401, got %v %v", res, err)
	}
}

func TestSameOrigin(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/vps/1/terminal", nil)
	r.Host = "127.0.0.1:7070"
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("Origin", "http://127.0.0.1:7070")
	if !sameOrigin(r) {
		t.Fatal("same origin refused")
	}
	// Through tailscale serve: browser host arrives as X-Forwarded-Host from loopback.
	r.Header.Set("Origin", "https://home-pc.tail1234.ts.net")
	r.Header.Set("X-Forwarded-For", "100.64.0.7")
	r.Header.Set("X-Forwarded-Host", "home-pc.tail1234.ts.net")
	if !sameOrigin(r) {
		t.Fatal("tailscale serve origin refused")
	}
	// The same headers from a non-loopback client are not trusted.
	r.RemoteAddr = "192.168.1.9:5000"
	if sameOrigin(r) {
		t.Fatal("forwarded host trusted from a non-loopback client")
	}
}
