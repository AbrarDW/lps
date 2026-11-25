package store

import (
	"context"
	"database/sql"
	"errors"
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
	Auto        bool      `json:"auto"` // taken by the schedule
	CreatedAt   time.Time `json:"createdAt"`
}

// Snapshot statuses.
const (
	SnapshotCreating = "creating"
	SnapshotReady    = "ready"
	SnapshotFailed   = "failed"
)

const snapCols = `id, vps_id, label, status, error, image, volume, system_bytes, home_bytes, auto, created_at`

func scanSnapshot(r interface{ Scan(...any) error }) (*Snapshot, error) {
	s := &Snapshot{}
	if err := r.Scan(&s.ID, &s.VPSID, &s.Label, &s.Status, &s.Error, &s.Image, &s.Volume, &s.SystemBytes, &s.HomeBytes, &s.Auto, &s.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return s, nil
}

// CreateSnapshot inserts a snapshot in "creating" state.
func (s *Store) CreateSnapshot(ctx context.Context, vpsID int64, label string) (*Snapshot, error) {
	return s.CreateSnapshotAuto(ctx, vpsID, label, false)
}

// CreateSnapshotAuto inserts a snapshot in "creating" state; auto marks one
// taken by the schedule.
func (s *Store) CreateSnapshotAuto(ctx context.Context, vpsID int64, label string, auto bool) (*Snapshot, error) {
	sn := &Snapshot{VPSID: vpsID, Label: label, Status: SnapshotCreating, Auto: auto, CreatedAt: time.Now().UTC()}
	res, err := s.db.ExecContext(ctx, `INSERT INTO vps_snapshots (vps_id, label, status, auto, created_at) VALUES (?, ?, ?, ?, ?)`,
		vpsID, label, sn.Status, auto, sn.CreatedAt)
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

// SnapshotSchedule takes snapshots of a VPS automatically.
type SnapshotSchedule struct {
	VPSID     int64      `json:"vpsId"`
	Frequency string     `json:"frequency"` // ScheduleDaily or ScheduleWeekly
	Hour      int        `json:"hour"`
	Weekday   int        `json:"weekday"` // 0 = Sunday
	Keep      int        `json:"keep"`
	LastRun   *time.Time `json:"lastRun,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// Schedule frequencies.
const (
	ScheduleDaily  = "daily"
	ScheduleWeekly = "weekly"
)

const schedCols = `vps_id, frequency, hour, weekday, keep, last_run, created_at`

func scanSchedule(r interface{ Scan(...any) error }) (*SnapshotSchedule, error) {
	sc := &SnapshotSchedule{}
	var last sql.NullTime
	if err := r.Scan(&sc.VPSID, &sc.Frequency, &sc.Hour, &sc.Weekday, &sc.Keep, &last, &sc.CreatedAt); err != nil {
		return nil, err
	}
	if last.Valid {
		t := last.Time
		sc.LastRun = &t
	}
	return sc, nil
}

// Schedule returns a VPS's snapshot schedule, or nil.
func (s *Store) Schedule(ctx context.Context, vpsID int64) (*SnapshotSchedule, error) {
	sc, err := scanSchedule(s.db.QueryRowContext(ctx, `SELECT `+schedCols+` FROM snapshot_schedules WHERE vps_id = ?`, vpsID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return sc, err
}

// Schedules lists all snapshot schedules.
func (s *Store) Schedules(ctx context.Context) ([]*SnapshotSchedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+schedCols+` FROM snapshot_schedules ORDER BY vps_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SnapshotSchedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// SetSchedule creates or changes a VPS's schedule. Changing it keeps
// last_run; a new schedule starts counting from now.
func (s *Store) SetSchedule(ctx context.Context, sc *SnapshotSchedule) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO snapshot_schedules (vps_id, frequency, hour, weekday, keep, created_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(vps_id) DO UPDATE SET frequency = excluded.frequency, hour = excluded.hour, weekday = excluded.weekday,
		keep = excluded.keep, created_at = excluded.created_at`,
		sc.VPSID, sc.Frequency, sc.Hour, sc.Weekday, sc.Keep, now)
	return err
}

// DeleteSchedule turns automatic snapshots off for a VPS.
func (s *Store) DeleteSchedule(ctx context.Context, vpsID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM snapshot_schedules WHERE vps_id = ?`, vpsID)
	return err
}

// SetScheduleRun records the slot a schedule last handled.
func (s *Store) SetScheduleRun(ctx context.Context, vpsID int64, slot time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE snapshot_schedules SET last_run = ? WHERE vps_id = ?`, slot.UTC(), vpsID)
	return err
}
