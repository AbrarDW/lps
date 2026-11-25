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
	// Image the VPS runs from after a restore or copy ("" = stock image).
	Image string `json:"-"`
}

const vpsCols = `id, name, ssh_user, ssh_port, ssh_public_key, cpus, memory_mb, container_id, created_at, image`

type scanner interface{ Scan(...any) error }

func scanVPS(r scanner) (*VPS, error) {
	v := &VPS{}
	err := r.Scan(&v.ID, &v.Name, &v.SSHUser, &v.SSHPort, &v.SSHPublicKey, &v.CPUs, &v.MemoryMB, &v.ContainerID, &v.CreatedAt, &v.Image)
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
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	SourcePath    string  `json:"sourcePath"`
	ContainerPort int     `json:"containerPort"`
	HostPort      int     `json:"hostPort"`
	CPUs          float64 `json:"cpus"`
	MemoryMB      int     `json:"memoryMb"`
	ContainerID   string  `json:"containerId"`
	PublicPort    int     `json:"publicPort"` // Funnel port; 0 = no public link
	Dockerfile    string  `json:"dockerfile"` // stored in LPS; "" = use the project's Dockerfile
	// VPSID is the VPS the app runs in; 0 = its own container.
	VPSID       int64  `json:"vpsId"`
	SourceKind  string `json:"sourceKind"` // SourcePC or SourceVPS
	VPSPath     string `json:"vpsPath"`    // code folder inside the VPS (SourceVPS)
	Packages    string `json:"packages"`   // apt packages the app needs (VPS apps)
	InstallCmd  string `json:"installCmd"` // run in the app folder on each deploy (VPS apps)
	StartCmd    string `json:"startCmd"`   // keeps running; LPS restarts it (VPS apps)
	WantRunning bool   `json:"wantRunning"`
	// Git source (SourceKind == SourceGit).
	GitURL     string `json:"gitUrl"`
	GitBranch  string `json:"gitBranch"`
	GitToken   string `json:"-"`
	AutoDeploy bool   `json:"autoDeploy"`
	LastCommit string `json:"lastCommit"`
	// QuickLink: a public Cloudflare quick link (trycloudflare.com) is on.
	QuickLink bool      `json:"quickLink"`
	CreatedAt time.Time `json:"createdAt"`
}

// App code sources.
const (
	SourcePC  = "pc"  // copied from a folder on this PC
	SourceVPS = "vps" // a folder that already exists inside the VPS
	SourceGit = "git" // cloned from a git repository on every deploy
)

// InVPS reports whether the app runs inside a VPS.
func (a *App) InVPS() bool { return a.VPSID > 0 }

const appCols = `id, name, source_path, container_port, host_port, cpus, memory_mb, container_id, public_port, dockerfile,
	vps_id, source_kind, vps_path, packages, install_cmd, start_cmd, want_running,
	git_url, git_branch, git_token, auto_deploy, last_commit, quick_link, created_at`

func scanApp(r scanner) (*App, error) {
	a := &App{}
	err := r.Scan(&a.ID, &a.Name, &a.SourcePath, &a.ContainerPort, &a.HostPort, &a.CPUs, &a.MemoryMB, &a.ContainerID, &a.PublicPort, &a.Dockerfile,
		&a.VPSID, &a.SourceKind, &a.VPSPath, &a.Packages, &a.InstallCmd, &a.StartCmd, &a.WantRunning,
		&a.GitURL, &a.GitBranch, &a.GitToken, &a.AutoDeploy, &a.LastCommit, &a.QuickLink, &a.CreatedAt)
	return a, err
}

// CreateApp inserts an app record.
func (s *Store) CreateApp(ctx context.Context, a *App) error {
	a.CreatedAt = time.Now().UTC()
	if a.SourceKind == "" {
		a.SourceKind = SourcePC
	}
	a.WantRunning = true
	res, err := s.db.ExecContext(ctx, `INSERT INTO apps (name, source_path, container_port, host_port, cpus, memory_mb, container_id, dockerfile,
		vps_id, source_kind, vps_path, packages, install_cmd, start_cmd, want_running,
		git_url, git_branch, git_token, auto_deploy, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`, a.Name, a.SourcePath, a.ContainerPort, a.HostPort, a.CPUs, a.MemoryMB, a.ContainerID, a.Dockerfile,
		a.VPSID, a.SourceKind, a.VPSPath, a.Packages, a.InstallCmd, a.StartCmd,
		a.GitURL, a.GitBranch, a.GitToken, a.AutoDeploy, a.CreatedAt)
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

// GetAppByName fetches an app by its name.
func (s *Store) GetAppByName(ctx context.Context, name string) (*App, error) {
	a, err := scanApp(s.db.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps WHERE name = ?`, name))
	if err != nil {
		return nil, mapErr(err)
	}
	return a, nil
}

// ListApps lists all apps.
func (s *Store) ListApps(ctx context.Context) ([]*App, error) {
	return s.queryApps(ctx, `SELECT `+appCols+` FROM apps ORDER BY id`)
}

// ListAppsByVPS lists the apps that run inside one VPS.
func (s *Store) ListAppsByVPS(ctx context.Context, vpsID int64) ([]*App, error) {
	return s.queryApps(ctx, `SELECT `+appCols+` FROM apps WHERE vps_id = ? ORDER BY id`, vpsID)
}

func (s *Store) queryApps(ctx context.Context, q string, args ...any) ([]*App, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
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

// SetAppDockerfile stores (or clears with "") an app's LPS-managed Dockerfile.
func (s *Store) SetAppDockerfile(ctx context.Context, id int64, dockerfile string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apps SET dockerfile = ? WHERE id = ?`, dockerfile, id)
	return err
}

// SetAppGit updates an app's branch, access token and auto-deploy setting.
func (s *Store) SetAppGit(ctx context.Context, id int64, branch, token string, autoDeploy bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apps SET git_branch = ?, git_token = ?, auto_deploy = ? WHERE id = ?`, branch, token, autoDeploy, id)
	return err
}

// SetAppLastCommit records the commit the running version was built from.
func (s *Store) SetAppLastCommit(ctx context.Context, id int64, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apps SET last_commit = ? WHERE id = ?`, hash, id)
	return err
}

// SetDeploymentCommit records which commit a deployment builds.
func (s *Store) SetDeploymentCommit(ctx context.Context, id int64, hash, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET commit_hash = ?, commit_message = ? WHERE id = ?`, hash, message, id)
	return err
}

// SetAppRunConfig replaces how a VPS app is set up and started.
func (s *Store) SetAppRunConfig(ctx context.Context, id int64, packages, install, start string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apps SET packages = ?, install_cmd = ?, start_cmd = ? WHERE id = ?`, packages, install, start, id)
	return err
}

// SetAppWantRunning records whether LPS should keep a VPS app running.
func (s *Store) SetAppWantRunning(ctx context.Context, id int64, want bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apps SET want_running = ? WHERE id = ?`, want, id)
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
	// The git commit built, for apps from a repository.
	CommitHash    string `json:"commitHash,omitempty"`
	CommitMessage string `json:"commitMessage,omitempty"`
}

// Deployment statuses.
const (
	DeployBuilding = "building"
	DeployRunning  = "running"
	// DeployReplaced is a deployment that ran until a newer one succeeded.
	DeployReplaced = "replaced"
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

// FinishDeployment records the outcome of a deployment. When it is now
// running, the app's earlier running deployments are marked replaced.
func (s *Store) FinishDeployment(ctx context.Context, id int64, status, image, log string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status = ?, image = ?, log = ?, finished_at = ? WHERE id = ?`,
		status, image, log, time.Now().UTC(), id); err != nil {
		return err
	}
	if status == DeployRunning {
		if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status = ?
			WHERE app_id = (SELECT app_id FROM deployments WHERE id = ?) AND id <> ? AND status = ?`,
			DeployReplaced, id, id, DeployRunning); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListDeployments returns an app's deployments, newest first.
func (s *Store) ListDeployments(ctx context.Context, appID int64, limit int) ([]*Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, app_id, status, image, log, started_at, finished_at, commit_hash, commit_message
		FROM deployments WHERE app_id = ? ORDER BY id DESC LIMIT ?`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Deployment{}
	for rows.Next() {
		d := &Deployment{}
		var fin sql.NullTime
		if err := rows.Scan(&d.ID, &d.AppID, &d.Status, &d.Image, &d.Log, &d.StartedAt, &fin, &d.CommitHash, &d.CommitMessage); err != nil {
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
