package store

import (
	"context"
	"time"
)

// Snapshot is a saved state of a VPS.
type Snapshot struct {
	ID          int64     `json:"id"`
	VPSID       int64     `json:"vpsId"`
	Label       string    `json:"label"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	Image       string    `json:"-"`
	Volume      string    `json:"-"`
	SystemBytes int64     `json:"systemBytes"`
	HomeBytes   int64     `json:"homeBytes"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Snapshot statuses.
const (
	SnapshotCreating = "creating"
	SnapshotReady    = "ready"
	SnapshotFailed   = "failed"
)

const snapCols = `id, vps_id, label, status, error, image, volume, system_bytes, home_bytes, created_at`

func scanSnapshot(r interface{ Scan(...any) error }) (*Snapshot, error) {
	s := &Snapshot{}
	if err := r.Scan(&s.ID, &s.VPSID, &s.Label, &s.Status, &s.Error, &s.Image, &s.Volume, &s.SystemBytes, &s.HomeBytes, &s.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return s, nil
}

// CreateSnapshot inserts a snapshot in "creating" state.
func (s *Store) CreateSnapshot(ctx context.Context, vpsID int64, label string) (*Snapshot, error) {
	sn := &Snapshot{VPSID: vpsID, Label: label, Status: SnapshotCreating, CreatedAt: time.Now().UTC()}
	res, err := s.db.ExecContext(ctx, `INSERT INTO vps_snapshots (vps_id, label, status, created_at) VALUES (?, ?, ?, ?)`,
		vpsID, label, sn.Status, sn.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	sn.ID, _ = res.LastInsertId()
	return sn, nil
}

// FinishSnapshot records the outcome of taking a snapshot.
func (s *Store) FinishSnapshot(ctx context.Context, sn *Snapshot) error {
	_, err := s.db.ExecContext(ctx, `UPDATE vps_snapshots SET status = ?, error = ?, image = ?, volume = ?, system_bytes = ?, home_bytes = ? WHERE id = ?`,
		sn.Status, sn.Error, sn.Image, sn.Volume, sn.SystemBytes, sn.HomeBytes, sn.ID)
	return err
}

// GetSnapshot fetches one snapshot.
func (s *Store) GetSnapshot(ctx context.Context, id int64) (*Snapshot, error) {
	return scanSnapshot(s.db.QueryRowContext(ctx, `SELECT `+snapCols+` FROM vps_snapshots WHERE id = ?`, id))
}

// ListSnapshots lists a VPS's snapshots, newest first.
func (s *Store) ListSnapshots(ctx context.Context, vpsID int64) ([]*Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+snapCols+` FROM vps_snapshots WHERE vps_id = ? ORDER BY id DESC`, vpsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Snapshot{}
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// DeleteSnapshot removes a snapshot record.
func (s *Store) DeleteSnapshot(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM vps_snapshots WHERE id = ?`, id)
	return err
}

// FailStaleSnapshots marks snapshots left "creating" by a restart as failed.
func (s *Store) FailStaleSnapshots(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE vps_snapshots SET status = ?, error = ? WHERE status = ?`,
		SnapshotFailed, "interrupted: LPS restarted while taking it", SnapshotCreating)
	return err
}

// SetVPSImage records the image a VPS runs from ("" = the stock image).
func (s *Store) SetVPSImage(ctx context.Context, id int64, image string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE vps SET image = ? WHERE id = ?`, image, id)
	return err
}
