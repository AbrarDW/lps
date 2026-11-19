package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Databases from before 0005 have several "running" deployments per app.
func TestReplacedDeploymentsMigration(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &App{Name: "x", SourcePath: "/x", ContainerPort: 80, HostPort: 8100}
	if err := s.CreateApp(ctx, a); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		d, _ := s.CreateDeployment(ctx, a.ID)
		// Simulate the old behaviour: set running without replacing.
		s.db.ExecContext(ctx, `UPDATE deployments SET status = 'running' WHERE id = ?`, d.ID)
	}
	if _, err := s.db.ExecContext(ctx, mustRead(t, "migrations/0005_replaced_deployments.sql")); err != nil {
		t.Fatal(err)
	}
	ds, _ := s.ListDeployments(ctx, a.ID, 10)
	if ds[0].Status != DeployRunning || ds[1].Status != DeployReplaced || ds[2].Status != DeployReplaced {
		t.Fatalf("statuses = %s %s %s", ds[0].Status, ds[1].Status, ds[2].Status)
	}
}

func mustRead(t *testing.T, name string) string {
	b, err := migrationFS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
