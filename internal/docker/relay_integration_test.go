package docker

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestRelayIntegration needs a Docker daemon, the VPS image and a running
// container "lps-vps-<LPS_IT_VPS>" serving HTTP on port 8080. Run with:
//
//	LPS_IT_VPS=tbox go test ./internal/docker -run Relay
func TestRelayIntegration(t *testing.T) {
	vps := os.Getenv("LPS_IT_VPS")
	if vps == "" {
		t.Skip("set LPS_IT_VPS to run against a real Docker daemon")
	}
	ctx := context.Background()
	c, err := NewClient("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureNetwork(ctx, VPSContainerName(vps)); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureNetwork(ctx, VPSContainerName(vps)); err != nil { // idempotent
		t.Fatal(err)
	}
	_ = c.Remove(ctx, RelayContainerName("it"))
	id, err := c.CreateRelay(ctx, RelaySpec{App: "it", VPS: vps, Port: 8080, HostPort: 18080})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Remove(ctx, id)
	if err := c.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for i := 0; i < 50; i++ {
		res, err := http.Get("http://127.0.0.1:18080/")
		if err == nil {
			body, _ = io.ReadAll(res.Body)
			res.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if string(body) != "hello from vps\n" {
		logs, _ := c.Logs(ctx, id, 50)
		t.Fatalf("body = %q; relay logs:\n%s", body, logs)
	}
	code, err := c.Exec(ctx, VPSContainerName(vps), "dev", "/", []string{"sh", "-c", "echo out; echo err >&2; exit 3"}, nil, io.Discard)
	if err != nil || code != 3 {
		t.Fatalf("exec code=%d err=%v", code, err)
	}
	if err := c.InstallAppHelper(ctx, VPSContainerName(vps)); err != nil {
		t.Fatal(err)
	}
}
