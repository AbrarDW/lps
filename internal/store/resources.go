package store

import (
	"context"
	"database/sql"
	"time"
)

// VPS is a long-lived Linux (Ubuntu) container reachable over SSH.
type VPS struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	SSHUser      string    `json:"sshUser"`
	SSHPort      int       `json:"sshPort"`
	SSHPublicKey string    `json:"sshPublicKey"`
	CPUs         float64   `json:"cpus"`
	MemoryMB     int       `json:"memoryMb"`
	ContainerID  string    `json:"containerId"`
	CreatedAt    time.Time `json:"createdAt"`
}

const vpsCols = `id, name, ssh_user, ssh_port, ssh_public_key, cpus, memory_mb, container_id, created_at`

type scanner interface{ Scan(...any) error }

func scanVPS(r scanner) (*VPS, error) {
	v := &VPS{}
	err := r.Scan(&v.ID, &v.Name, &v.SSHUser, &v.SSHPort, &v.SSHPublicKey, &v.CPUs, &v.MemoryMB, &v.ContainerID, &v.CreatedAt)
	return v, err
}

// CreateVPS inserts a VPS record.
func (s *Store) CreateVPS(ctx context.Context, v *VPS) error {
	v.CreatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO vps (name, ssh_user, ssh_port, ssh_public_key, cpus, memory_mb, container_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, v.Name, v.SSHUser, v.SSHPort, v.SSHPublicKey, v.CPUs, v.MemoryMB, v.ContainerID, v.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	v.ID, _ = res.LastInsertId()
	return nil
}

// GetVPS fetches one VPS.
func (s *Store) GetVPS(ctx context.Context, id int64) (*VPS, error) {
	v, err := scanVPS(s.db.QueryRowContext(ctx, `SELECT `+vpsCols+` FROM vps WHERE id = ?`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return v, nil
}

// ListVPS lists all VPSes.
func (s *Store) ListVPS(ctx context.Context) ([]*VPS, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+vpsCols+` FROM vps ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*VPS{}
	for rows.Next() {
		v, err := scanVPS(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetVPSContainer records the Docker container backing a VPS.
func (s *Store) SetVPSContainer(ctx context.Context, id int64, containerID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE vps SET container_id = ? WHERE id = ?`, containerID, id)
	return err
}

// DeleteVPS removes a VPS record.
func (s *Store) DeleteVPS(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM vps WHERE id = ?`, id)
	return err
}

// App is a directory with a Dockerfile deployed as a container.
type App struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	SourcePath    string    `json:"sourcePath"`
	ContainerPort int       `json:"containerPort"`
	HostPort      int       `json:"hostPort"`
	CPUs          float64   `json:"cpus"`
	MemoryMB      int       `json:"memoryMb"`
	ContainerID   string    `json:"containerId"`
	CreatedAt     time.Time `json:"createdAt"`
}

const appCols = `id, name, source_path, container_port, host_port, cpus, memory_mb, container_id, created_at`

func scanApp(r scanner) (*App, error) {
	a := &App{}
	err := r.Scan(&a.ID, &a.Name, &a.SourcePath, &a.ContainerPort, &a.HostPort, &a.CPUs, &a.MemoryMB, &a.ContainerID, &a.CreatedAt)
	return a, err
}

// CreateApp inserts an app record.
func (s *Store) CreateApp(ctx context.Context, a *App) error {
	a.CreatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO apps (name, source_path, container_port, host_port, cpus, memory_mb, container_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, a.Name, a.SourcePath, a.ContainerPort, a.HostPort, a.CPUs, a.MemoryMB, a.ContainerID, a.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

// GetApp fetches one app.
func (s *Store) GetApp(ctx context.Context, id int64) (*App, error) {
	a, err := scanApp(s.db.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps WHERE id = ?`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return a, nil
}

// ListApps lists all apps.
func (s *Store) ListApps(ctx context.Context) ([]*App, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+appCols+` FROM apps ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAppContainer records the container currently serving an app.
func (s *Store) SetAppContainer(ctx context.Context, id int64, containerID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apps SET container_id = ? WHERE id = ?`, containerID, id)
	return err
}

// DeleteApp removes an app and its deployments.
func (s *Store) DeleteApp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id)
	return err
}

// UsedPorts returns every host port allocated to a VPS (SSH) or app.
func (s *Store) UsedPorts(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ssh_port FROM vps UNION SELECT host_port FROM apps`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		used[p] = true
	}
	return used, rows.Err()
}

// Deployment is one build+run attempt of an app.
type Deployment struct {
	ID         int64      `json:"id"`
	AppID      int64      `json:"appId"`
	Status     string     `json:"status"`
	Image      string     `json:"image"`
	Log        string     `json:"log"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

// Deployment statuses.
const (
	DeployBuilding = "building"
	DeployRunning  = "running"
	DeployFailed   = "failed"
)

// CreateDeployment starts a deployment record in "building" state.
func (s *Store) CreateDeployment(ctx context.Context, appID int64) (*Deployment, error) {
	d := &Deployment{AppID: appID, Status: DeployBuilding, StartedAt: time.Now().UTC()}
	res, err := s.db.ExecContext(ctx, `INSERT INTO deployments (app_id, status, started_at) VALUES (?, ?, ?)`, appID, d.Status, d.StartedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	d.ID, _ = res.LastInsertId()
	return d, nil
}

// FinishDeployment records the outcome of a deployment.
func (s *Store) FinishDeployment(ctx context.Context, id int64, status, image, log string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET status = ?, image = ?, log = ?, finished_at = ? WHERE id = ?`,
		status, image, log, time.Now().UTC(), id)
	return err
}

// ListDeployments returns an app's deployments, newest first.
func (s *Store) ListDeployments(ctx context.Context, appID int64, limit int) ([]*Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, app_id, status, image, log, started_at, finished_at
		FROM deployments WHERE app_id = ? ORDER BY id DESC LIMIT ?`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Deployment{}
	for rows.Next() {
		d := &Deployment{}
		var fin sql.NullTime
		if err := rows.Scan(&d.ID, &d.AppID, &d.Status, &d.Image, &d.Log, &d.StartedAt, &fin); err != nil {
			return nil, err
		}
		if fin.Valid {
			t := fin.Time
			d.FinishedAt = &t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// FailStaleDeployments marks deployments left "building" by a crash/restart as failed.
func (s *Store) FailStaleDeployments(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET status = ?, log = log || ?, finished_at = ? WHERE status = ?`,
		DeployFailed, "\n[lps] interrupted: lpsd restarted during build\n", time.Now().UTC(), DeployBuilding)
	return err
}
