package api

// The apps gateway gives every app a name-based address instead of a port:
//
//   - On this PC: http://<app>.localhost:<dashboard port>. Browsers resolve
//     *.localhost to this machine, and lpsd routes by the Host header.
//   - From your devices: https://<pc>.ts.net:8080/<app>/. Tailscale publishes
//     the gateway listener, which routes by the first path segment.
//
// The gateway is never served from the dashboard's origin: an app's scripts
// must not be able to call the LPS API with the user's session. App names
// on the dashboard port only answer to <app>.localhost hosts, and the
// path-based gateway has its own port. The dashboard's session cookie is
// stripped from every proxied request, since cookies ignore ports.

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/abrardw/lps/internal/store"
)

// appsRemotePort is where the path-based gateway is published on the tailnet.
const appsRemotePort = 8080

// appHostName returns the app name for hosts like "site.localhost:7070".
func appHostName(host string) (string, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	name, ok := strings.CutSuffix(host, ".localhost")
	if !ok || !nameRe.MatchString(name) {
		return "", false
	}
	return name, true
}

// appTarget is where an app's port is reachable from lpsd.
func (s *Server) appTarget(a *store.App) *url.URL {
	ip := s.cfg.BindIP
	if ip == "" || ip == "0.0.0.0" || ip == "::" {
		ip = "127.0.0.1"
	}
	return &url.URL{Scheme: "http", Host: net.JoinHostPort(ip, strconv.Itoa(a.HostPort))}
}

// withoutSessionCookie drops the dashboard's session cookie so an app never
// sees it (cookies are shared across ports of the same host).
func withoutSessionCookie(h http.Header) {
	cookies := (&http.Request{Header: http.Header{"Cookie": h.Values("Cookie")}}).Cookies()
	h.Del("Cookie")
	var keep []string
	for _, c := range cookies {
		if c.Name != sessionCookie {
			keep = append(keep, c.Name+"="+c.Value)
		}
	}
	if len(keep) > 0 {
		h.Set("Cookie", strings.Join(keep, "; "))
	}
}

// proxyApp forwards a request to an app. prefix is the path the app is
// mounted under ("" for host-based routing), and is stripped on the way in
// and added back to redirects on the way out.
func (s *Server) proxyApp(w http.ResponseWriter, r *http.Request, a *store.App, prefix string) {
	target := s.appTarget(a)
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			if prefix != "" {
				p := strings.TrimPrefix(pr.In.URL.Path, prefix)
				if !strings.HasPrefix(p, "/") {
					p = "/" + p
				}
				pr.Out.URL.Path = p
				pr.Out.URL.RawPath = ""
				pr.Out.Header.Set("X-Forwarded-Prefix", prefix)
			}
			pr.SetXForwarded()
			// Behind Tailscale serve the outer proxy already set these.
			if viaLocalProxy(pr.In) {
				if v := pr.In.Header.Get("X-Forwarded-Proto"); v != "" {
					pr.Out.Header.Set("X-Forwarded-Proto", v)
				}
				if v := pr.In.Header.Get("X-Forwarded-Host"); v != "" {
					pr.Out.Header.Set("X-Forwarded-Host", v)
				}
			}
			pr.Out.Host = pr.In.Host // apps see the address the user typed
			withoutSessionCookie(pr.Out.Header)
		},
		ModifyResponse: func(res *http.Response) error {
			if prefix == "" {
				return nil
			}
			// Keep redirects inside the app's path: "/login" → "/app/login".
			loc := res.Header.Get("Location")
			if loc == "" {
				return nil
			}
			if u, err := url.Parse(loc); err == nil {
				if u.Host == target.Host { // absolute redirect to the internal address
					u.Scheme, u.Host = "", ""
					loc = u.String()
				}
			}
			if strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") && loc != prefix && !strings.HasPrefix(loc, prefix+"/") {
				res.Header.Set("Location", prefix+loc)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			gatewayPage(w, http.StatusBadGateway, "This app isn't answering",
				fmt.Sprintf("LPS could not reach <b>%s</b>. Is it running? Check its page in the LPS dashboard.", html.EscapeString(a.Name)))
		},
	}
	rp.ServeHTTP(w, r)
}

// serveAppByHost handles <app>.localhost requests on the dashboard listener.
// It reports false when the host isn't an app host, so the dashboard serves it.
func (s *Server) serveAppByHost(w http.ResponseWriter, r *http.Request) bool {
	name, ok := appHostName(r.Host)
	if !ok {
		return false
	}
	a, err := s.store.GetAppByName(r.Context(), name)
	if err != nil {
		gatewayPage(w, http.StatusNotFound, "No such app",
			fmt.Sprintf("There is no app called <b>%s</b> on this PC.", html.EscapeString(name)))
		return true
	}
	s.proxyApp(w, r, a, "")
	return true
}

// GatewayHandler serves the path-based apps gateway: /<app>/… goes to the app.
func (s *Server) GatewayHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seg, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if seg == "" {
			s.gatewayIndex(w, r)
			return
		}
		a, err := s.store.GetAppByName(r.Context(), seg)
		if err != nil {
			gatewayPage(w, http.StatusNotFound, "No such app",
				fmt.Sprintf("There is no app called <b>%s</b> here. <a href=\"/\">See all apps</a>.", html.EscapeString(seg)))
			return
		}
		prefix := "/" + a.Name
		if r.URL.Path == prefix {
			target := prefix + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		s.proxyApp(w, r, a, prefix)
	})
}

// gatewayIndex lists the apps (names only) so the root isn't a dead end.
func (s *Server) gatewayIndex(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.ListApps(r.Context())
	if err != nil {
		gatewayPage(w, http.StatusInternalServerError, "Error", "Could not list apps.")
		return
	}
	var b strings.Builder
	if len(apps) == 0 {
		b.WriteString("No apps yet.")
	} else {
		b.WriteString("<ul>")
		for _, a := range apps {
			n := html.EscapeString(a.Name)
			fmt.Fprintf(&b, `<li><a href="/%s/">%s</a></li>`, n, n)
		}
		b.WriteString("</ul>")
	}
	gatewayPage(w, http.StatusOK, "Apps on this PC", b.String())
}

// gatewayPage writes a small self-contained page (bodyHTML must be escaped).
func gatewayPage(w http.ResponseWriter, status int, title, bodyHTML string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s · LPS</title>
<style>body{margin:0;min-height:100vh;background:#0a2440;color:#e6f0fa;font:15px/1.5 system-ui,sans-serif;display:grid;place-items:center}
main{max-width:34rem;padding:24px;border:1px solid #24507f;background:#0e2d4e;border-radius:3px}
h1{font-size:20px;margin:0 0 8px}a{color:#5cc8ff}small{color:#8fafd0;font:11px monospace;letter-spacing:.12em;text-transform:uppercase}</style>
<main><small>LPS · apps</small><h1>%s</h1><p>%s</p></main>`, html.EscapeString(title), html.EscapeString(title), bodyHTML)
}

// localAppURL is an app's name-based address on this PC.
func (s *Server) localAppURL(a *store.App) string {
	if s.cfg.DashboardPort == 0 {
		return ""
	}
	return fmt.Sprintf("http://%s.localhost:%d", a.Name, s.cfg.DashboardPort)
}
