package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	for i := 0; i < 2; i++ {
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		s.Close()
	}
}

func TestFirstUserOnlyOnce(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := s.CreateFirstUser(ctx, "admin", "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFirstUser(ctx, "other", "h"); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if n, _ := s.CountUsers(ctx); n != 1 {
		t.Fatalf("users = %d", n)
	}
}

func TestSessions(t *testing.T) {
	s, ctx := open(t), context.Background()
	u, _ := s.CreateFirstUser(ctx, "admin", "h")
	if err := s.CreateSession(ctx, "live", u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, "old", u.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.UserBySession(ctx, "live"); err != nil || got.Username != "admin" {
		t.Fatalf("live session: %v %v", got, err)
	}
	if _, err := s.UserBySession(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session should be not found, got %v", err)
	}
	s.DeleteSession(ctx, "live")
	if _, err := s.UserBySession(ctx, "live"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session still valid: %v", err)
	}
}

func TestVPSAndAppsAndPorts(t *testing.T) {
	s, ctx := open(t), context.Background()
	v := &VPS{Name: "dev", SSHUser: "me", SSHPort: 2201, CPUs: 1, MemoryMB: 512}
	if err := s.CreateVPS(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateVPS(ctx, &VPS{Name: "dev", SSHUser: "me", SSHPort: 2202}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	a := &App{Name: "web", SourcePath: "/src", ContainerPort: 80, HostPort: 8100}
	if err := s.CreateApp(ctx, a); err != nil {
		t.Fatal(err)
	}
	used, _ := s.UsedPorts(ctx)
	if !used[2201] || !used[8100] || len(used) != 2 {
		t.Fatalf("used ports = %v", used)
	}
	d, _ := s.CreateDeployment(ctx, a.ID)
	s.FinishDeployment(ctx, d.ID, DeployRunning, "img", "log")
	ds, _ := s.ListDeployments(ctx, a.ID, 5)
	if len(ds) != 1 || ds[0].Status != DeployRunning || ds[0].FinishedAt == nil {
		t.Fatalf("deployments = %+v", ds)
	}
	// Deleting the app cascades to deployments.
	s.DeleteApp(ctx, a.ID)
	if ds, _ := s.ListDeployments(ctx, a.ID, 5); len(ds) != 0 {
		t.Fatalf("deployments not cascaded: %d", len(ds))
	}
}

func TestFailStaleDeployments(t *testing.T) {
	s, ctx := open(t), context.Background()
	a := &App{Name: "web", SourcePath: "/src", ContainerPort: 80, HostPort: 8100}
	s.CreateApp(ctx, a)
	s.CreateDeployment(ctx, a.ID)
	s.FailStaleDeployments(ctx)
	ds, _ := s.ListDeployments(ctx, a.ID, 5)
	if ds[0].Status != DeployFailed {
		t.Fatalf("status = %s", ds[0].Status)
	}
}

func TestRemoteSettingsAndExposures(t *testing.T) {
	s, ctx := open(t), context.Background()
	if v, _ := s.GetSetting(ctx, SettingRemoteEnabled, "false"); v != "false" {
		t.Fatalf("default = %q", v)
	}
	s.SetSetting(ctx, SettingRemoteEnabled, "true")
	s.SetSetting(ctx, SettingRemoteEnabled, "true")
	if v, _ := s.GetSetting(ctx, SettingRemoteEnabled, "false"); v != "true" {
		t.Fatalf("after set = %q", v)
	}
	s.SaveExposure(ctx, AppliedExposure{Proto: "https", Port: 443, LocalPort: 7070})
	s.SaveExposure(ctx, AppliedExposure{Proto: "https", Port: 443, LocalPort: 7071, Public: false})
	s.SaveExposure(ctx, AppliedExposure{Proto: "tcp", Port: 2201, LocalPort: 2201})
	es, _ := s.ListExposures(ctx)
	if len(es) != 2 || es[0].LocalPort != 7071 {
		t.Fatalf("exposures = %+v", es)
	}
	s.DeleteExposure(ctx, "tcp", 2201)
	if es, _ := s.ListExposures(ctx); len(es) != 1 {
		t.Fatalf("after delete = %+v", es)
	}
	a := &App{Name: "web", SourcePath: "/src", ContainerPort: 80, HostPort: 8100}
	s.CreateApp(ctx, a)
	s.SetAppPublicPort(ctx, a.ID, 8443)
	if got, _ := s.GetApp(ctx, a.ID); got.PublicPort != 8443 {
		t.Fatalf("public port = %d", got.PublicPort)
	}
}
