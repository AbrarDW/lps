package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/abrardw/lps/internal/store"
)

// Activity-feed subjects.
const (
	subjVPS    = "vps"
	subjApp    = "app"
	subjRemote = "remote"
	subjAuth   = "auth"
)

// record adds an event to the activity feed. r is the request that caused it
// (for who and from where), or nil when LPS itself acted.
func (s *Server) record(r *http.Request, level, kind, subject string, id int64, name, format string, args ...any) {
	e := &store.Event{Level: level, Kind: kind, Subject: subject, SubjectID: id, Name: name, Message: fmt.Sprintf(format, args...)}
	if r != nil {
		if u, ok := r.Context().Value(userKey).(*store.User); ok && u != nil {
			e.Actor = u.Username
		}
		e.IP = clientIP(r)
	}
	s.addEvent(e)
}

// addEvent stores e; recording never fails the action that caused it.
func (s *Server) addEvent(e *store.Event) {
	if err := s.store.AddEvent(context.WithoutCancel(s.ctx), e); err != nil {
		s.log.Warn("record event", "kind", e.Kind, "err", err)
	}
}

// info / warn / fail are shorthands for record at each level.
func (s *Server) info(r *http.Request, kind, subject string, id int64, name, format string, args ...any) {
	s.record(r, store.LevelInfo, kind, subject, id, name, format, args...)
}

func (s *Server) warn(r *http.Request, kind, subject string, id int64, name, format string, args ...any) {
	s.record(r, store.LevelWarning, kind, subject, id, name, format, args...)
}

func (s *Server) fail(r *http.Request, kind, subject string, id int64, name, format string, args ...any) {
	s.record(r, store.LevelError, kind, subject, id, name, format, args...)
}

// firstLine keeps event messages to one short line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// handleListEvents serves the activity feed: ?subject=vps|app|remote|auth&id=N&before=ID&limit=N
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EventFilter{Subject: q.Get("subject")}
	f.SubjectID, _ = strconv.ParseInt(q.Get("id"), 10, 64)
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	switch f.Subject {
	case "", subjVPS, subjApp, subjRemote, subjAuth:
	default:
		writeError(w, 400, "subject must be vps, app, remote or auth")
		return
	}
	list, err := s.store.ListEvents(r.Context(), f)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, list)
}
