package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Cloudflare connection settings (custom domains).
const (
	SettingCFToken       = "cloudflare.token"        // API token; never returned by the API
	SettingCFAccount     = "cloudflare.account"      // account id
	SettingCFAccountName = "cloudflare.account_name" // shown in the UI
	SettingCFTunnel      = "cloudflare.tunnel"       // tunnel id
	SettingCFTunnelName  = "cloudflare.tunnel_name"
	SettingCFTunnelToken = "cloudflare.tunnel_token" // token cloudflared runs with
)

// CloudflareSettings is the saved Cloudflare connection.
type CloudflareSettings struct {
	Token, Account, AccountName, Tunnel, TunnelName, TunnelToken string
}

// Connected reports whether a tunnel has been set up.
func (c CloudflareSettings) Connected() bool {
	return c.Token != "" && c.Tunnel != "" && c.TunnelToken != ""
}

var cfKeys = []string{SettingCFToken, SettingCFAccount, SettingCFAccountName, SettingCFTunnel, SettingCFTunnelName, SettingCFTunnelToken}

func (c *CloudflareSettings) fields() []*string {
	return []*string{&c.Token, &c.Account, &c.AccountName, &c.Tunnel, &c.TunnelName, &c.TunnelToken}
}

// Cloudflare returns the saved Cloudflare connection (empty when not connected).
func (s *Store) Cloudflare(ctx context.Context) (CloudflareSettings, error) {
	var c CloudflareSettings
	for i, f := range c.fields() {
		v, err := s.GetSetting(ctx, cfKeys[i], "")
		if err != nil {
			return c, err
		}
		*f = v
	}
	return c, nil
}

// SetCloudflare saves the Cloudflare connection; an empty value clears it.
func (s *Store) SetCloudflare(ctx context.Context, c CloudflareSettings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, f := range c.fields() {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, cfKeys[i], *f); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AppDomain is an app's address on the user's own domain.
type AppDomain struct {
	AppID     int64     `json:"appId"`
	Hostname  string    `json:"hostname"`
	ZoneID    string    `json:"-"`
	RecordID  string    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
}

// ErrDomainTaken is returned when another app already uses the hostname.
var ErrDomainTaken = errors.New("another app already uses this address")

// SaveAppDomain sets an app's domain (replacing any it had).
func (s *Store) SaveAppDomain(ctx context.Context, d *AppDomain) error {
	d.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_domains (app_id, hostname, zone_id, record_id, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(app_id) DO UPDATE SET hostname = excluded.hostname, zone_id = excluded.zone_id, record_id = excluded.record_id, created_at = excluded.created_at`,
		d.AppID, d.Hostname, d.ZoneID, d.RecordID, d.CreatedAt)
	if errors.Is(mapErr(err), ErrConflict) {
		return ErrDomainTaken
	}
	return err
}

// AppDomain returns an app's domain, or nil.
func (s *Store) AppDomain(ctx context.Context, appID int64) (*AppDomain, error) {
	d := &AppDomain{}
	err := s.db.QueryRowContext(ctx, `SELECT app_id, hostname, zone_id, record_id, created_at FROM app_domains WHERE app_id = ?`, appID).
		Scan(&d.AppID, &d.Hostname, &d.ZoneID, &d.RecordID, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// AppDomains lists all app domains.
func (s *Store) AppDomains(ctx context.Context) ([]*AppDomain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT app_id, hostname, zone_id, record_id, created_at FROM app_domains ORDER BY hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AppDomain{}
	for rows.Next() {
		d := &AppDomain{}
		if err := rows.Scan(&d.AppID, &d.Hostname, &d.ZoneID, &d.RecordID, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteAppDomain removes an app's domain.
func (s *Store) DeleteAppDomain(ctx context.Context, appID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_domains WHERE app_id = ?`, appID)
	return err
}

// DeleteAllAppDomains removes every app domain.
func (s *Store) DeleteAllAppDomains(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_domains`)
	return err
}
