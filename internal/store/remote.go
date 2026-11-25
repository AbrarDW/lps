package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Setting keys.
const (
	SettingRemoteEnabled = "remote.enabled"
)

// GetSetting returns a setting or def when unset.
func (s *Store) GetSetting(ctx context.Context, key, def string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	return v, err
}

// SetSetting stores a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// AppliedExposure is an exposure LPS created on the remote-access provider.
type AppliedExposure struct {
	Proto     string
	Port      int
	LocalPort int
	Public    bool
	AppliedAt time.Time
}

// ListExposures returns all exposures LPS has applied.
func (s *Store) ListExposures(ctx context.Context) ([]AppliedExposure, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT proto, port, local_port, public, applied_at FROM exposures ORDER BY port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppliedExposure
	for rows.Next() {
		var e AppliedExposure
		if err := rows.Scan(&e.Proto, &e.Port, &e.LocalPort, &e.Public, &e.AppliedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveExposure records an applied exposure (replacing one on the same port).
func (s *Store) SaveExposure(ctx context.Context, e AppliedExposure) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO exposures (proto, port, local_port, public, applied_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(proto, port) DO UPDATE SET local_port = excluded.local_port, public = excluded.public, applied_at = excluded.applied_at`,
		e.Proto, e.Port, e.LocalPort, e.Public, time.Now().UTC())
	return err
}

// DeleteExposure forgets an exposure.
func (s *Store) DeleteExposure(ctx context.Context, proto string, port int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM exposures WHERE proto = ? AND port = ?`, proto, port)
	return err
}

// SetAppPublicPort sets (or clears with 0) an app's public link port.
func (s *Store) SetAppPublicPort(ctx context.Context, id int64, port int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apps SET public_port = ? WHERE id = ?`, port, id)
	return err
}

// SetAppQuickLink turns an app's Cloudflare quick link on or off.
func (s *Store) SetAppQuickLink(ctx context.Context, id int64, on bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apps SET quick_link = ? WHERE id = ?`, on, id)
	return err
}
