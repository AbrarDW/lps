package store

import (
	"context"
	"strings"
	"time"
)

// Event is one entry in the activity feed.
type Event struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Level     string    `json:"level"`
	Kind      string    `json:"kind"`
	Subject   string    `json:"subject"`
	SubjectID int64     `json:"subjectId"`
	Name      string    `json:"name"`
	Actor     string    `json:"actor"`
	IP        string    `json:"ip"`
	Message   string    `json:"message"`
}

// Event levels.
const (
	LevelInfo    = "info"
	LevelWarning = "warning"
	LevelError   = "error"
)

// maxEvents is how many events are kept; older ones are pruned.
const maxEvents = 2000

// AddEvent stores an event and prunes the oldest beyond maxEvents.
func (s *Store) AddEvent(ctx context.Context, e *Event) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	if e.Level == "" {
		e.Level = LevelInfo
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO events (at, level, kind, subject, subject_id, name, actor, ip, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.At, e.Level, e.Kind, e.Subject, e.SubjectID, e.Name, e.Actor, e.IP, e.Message)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	if e.ID%100 == 0 {
		_, err = s.db.ExecContext(ctx, `DELETE FROM events WHERE id <= ?`, e.ID-maxEvents)
	}
	return err
}

// EventFilter narrows ListEvents. Zero values match everything.
type EventFilter struct {
	Subject   string // vps | app | remote | auth | system
	SubjectID int64
	Before    int64 // only events with id < Before (paging)
	Limit     int
}

// ListEvents returns events newest first.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]*Event, error) {
	where, args := []string{"1=1"}, []any{}
	if f.Subject != "" {
		where, args = append(where, "subject = ?"), append(args, f.Subject)
	}
	if f.SubjectID > 0 {
		where, args = append(where, "subject_id = ?"), append(args, f.SubjectID)
	}
	if f.Before > 0 {
		where, args = append(where, "id < ?"), append(args, f.Before)
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, level, kind, subject, subject_id, name, actor, ip, message
		FROM events WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Event{}
	for rows.Next() {
		e := &Event{}
		if err := rows.Scan(&e.ID, &e.At, &e.Level, &e.Kind, &e.Subject, &e.SubjectID, &e.Name, &e.Actor, &e.IP, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
