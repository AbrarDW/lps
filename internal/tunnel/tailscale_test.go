package tunnel

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseStatus(t *testing.T) {
	st, err := ParseStatus(fixture(t, "status_logged_out.json"))
	if err != nil || st.Running || st.State != "NeedsLogin" || st.HTTPSEnabled {
		t.Fatalf("logged out: %+v %v", st, err)
	}

	st, _ = ParseStatus(fixture(t, "status_no_https.json"))
	if !st.Running || st.DNSName != "home-pc.tail1234.ts.net" || st.HTTPSEnabled || st.FunnelEnabled || st.Tailnet != "you@example.com" {
		t.Fatalf("no https: %+v", st)
	}

	st, _ = ParseStatus(fixture(t, "status_ready.json"))
	if !st.HTTPSEnabled || !st.FunnelEnabled || !reflect.DeepEqual(st.FunnelPorts, []int{443, 8443, 10000}) {
		t.Fatalf("ready: %+v", st)
	}
}

func TestParseServePorts(t *testing.T) {
	ports, err := ParseServePorts(fixture(t, "serve_status.json"))
	if err != nil || !ports[443] || !ports[2201] || len(ports) != 2 {
		t.Fatalf("ports=%v err=%v", ports, err)
	}
	if p, err := ParseServePorts([]byte("{}\n")); err != nil || len(p) != 0 {
		t.Fatalf("empty: %v %v", p, err)
	}
}

func TestArgs(t *testing.T) {
	cases := []struct {
		e          Exposure
		apply, rmv string
	}{
		{Exposure{ProtoHTTPS, 443, 7070, false}, "serve --bg --yes --https=443 http://127.0.0.1:7070", "serve --yes --https=443 off"},
		{Exposure{ProtoTCP, 2201, 2201, false}, "serve --bg --yes --tcp=2201 tcp://127.0.0.1:2201", "serve --yes --tcp=2201 off"},
		{Exposure{ProtoHTTPS, 8443, 8100, true}, "funnel --bg --yes --https=8443 http://127.0.0.1:8100", "funnel --yes --https=8443 off"},
	}
	for _, c := range cases {
		if got := strings.Join(ApplyArgs(c.e), " "); got != c.apply {
			t.Errorf("apply %v: %s", c.e, got)
		}
		if got := strings.Join(RemoveArgs(c.e), " "); got != c.rmv {
			t.Errorf("remove %v: %s", c.e, got)
		}
	}
}

func TestCheckReady(t *testing.T) {
	noHTTPS, _ := ParseStatus(fixture(t, "status_no_https.json"))
	ready, _ := ParseStatus(fixture(t, "status_ready.json"))
	tcp := Exposure{ProtoTCP, 2201, 2201, false}
	web := Exposure{ProtoHTTPS, 443, 7070, false}
	pub := Exposure{ProtoHTTPS, 8443, 8100, true}

	if err := CheckReady(Status{}, tcp); err == nil {
		t.Fatal("not installed must need action")
	}
	if err := CheckReady(noHTTPS, tcp); err != nil {
		t.Fatalf("tcp works without https: %v", err)
	}
	if na, ok := AsNeedsAction(CheckReady(noHTTPS, web)); !ok || na.URL != TailscaleHTTPSURL {
		t.Fatalf("https needs enabling: %v", na)
	}
	if err := CheckReady(ready, pub); err != nil {
		t.Fatal(err)
	}
	if err := CheckReady(ready, Exposure{ProtoHTTPS, 9000, 8100, true}); err == nil {
		t.Fatal("port 9000 is not a funnel port")
	}
}

// fakeCLI emulates the tailscale CLI over an in-memory serve config.
type fakeCLI struct {
	mu        sync.Mutex
	status    []byte
	ports     map[int]bool
	calls     []string
	enableURL bool // print an enable URL and exit 0 without applying
}

func (f *fakeCLI) run(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case args[0] == "status":
		return f.status, nil, nil
	case len(args) > 1 && args[1] == "status":
		s := "{\"TCP\":{"
		first := true
		for p := range f.ports {
			if !first {
				s += ","
			}
			s += "\"" + strconv.Itoa(p) + "\":{}"
			first = false
		}
		return []byte(s + "}}"), nil, nil
	}
	var port int
	off := args[len(args)-1] == "off"
	for _, a := range args {
		if strings.HasPrefix(a, "--https=") || strings.HasPrefix(a, "--tcp=") {
			port, _ = strconv.Atoi(a[strings.Index(a, "=")+1:])
		}
	}
	if off {
		if !f.ports[port] {
			return nil, []byte("error: handler does not exist"), errors.New("exit status 1")
		}
		delete(f.ports, port)
		return nil, nil, nil
	}
	if f.enableURL {
		return []byte("Serve is not enabled on your tailnet.\nTo enable, visit:\n\n         https://login.tailscale.com/f/serve?node=abc\n"), nil, nil
	}
	f.ports[port] = true
	return []byte("Available within your tailnet"), nil, nil
}

func TestApplyAndRemove(t *testing.T) {
	f := &fakeCLI{status: fixture(t, "status_ready.json"), ports: map[int]bool{}}
	ts := NewTailscaleWith("tailscale", f.run)
	ctx := context.Background()
	e := Exposure{ProtoHTTPS, 443, 7070, false}
	if err := ts.Apply(ctx, e); err != nil {
		t.Fatal(err)
	}
	if !f.ports[443] {
		t.Fatal("not applied")
	}
	if err := ts.Remove(ctx, e); err != nil {
		t.Fatal(err)
	}
	// Removing again is fine.
	if err := ts.Remove(ctx, e); err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
}

func TestApplyDetectsEnableURL(t *testing.T) {
	f := &fakeCLI{status: fixture(t, "status_ready.json"), ports: map[int]bool{}, enableURL: true}
	ts := NewTailscaleWith("tailscale", f.run)
	err := ts.Apply(context.Background(), Exposure{ProtoHTTPS, 443, 7070, false})
	na, ok := AsNeedsAction(err)
	if !ok || na.URL != "https://login.tailscale.com/f/serve?node=abc" {
		t.Fatalf("want needs-action with URL, got %v", err)
	}
}

func TestApplyRequiresLogin(t *testing.T) {
	f := &fakeCLI{status: fixture(t, "status_logged_out.json"), ports: map[int]bool{}}
	ts := NewTailscaleWith("tailscale", f.run)
	if _, ok := AsNeedsAction(ts.Apply(context.Background(), Exposure{ProtoTCP, 2201, 2201, false})); !ok {
		t.Fatal("expected needs-action when logged out")
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "serve --bg") {
			t.Fatal("must not call serve while logged out")
		}
	}
}

func TestNotInstalled(t *testing.T) {
	ts := NewTailscaleWith("", nil)
	st, err := ts.Status(context.Background())
	if err != nil || st.Installed {
		t.Fatalf("status: %+v %v", st, err)
	}
}

func TestStatusIsCached(t *testing.T) {
	f := &fakeCLI{status: fixture(t, "status_ready.json"), ports: map[int]bool{}}
	ts := NewTailscaleWith("tailscale", f.run)
	ts.statusTTL = time.Hour
	ctx := context.Background()
	countStatus := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		n := 0
		for _, c := range f.calls {
			if strings.HasPrefix(c, "status") {
				n++
			}
		}
		return n
	}
	for i := 0; i < 5; i++ {
		if st, err := ts.Status(ctx); err != nil || !st.Running {
			t.Fatalf("status: %+v %v", st, err)
		}
	}
	if n := countStatus(); n != 1 {
		t.Fatalf("tailscale status ran %d times, want 1 (cached)", n)
	}
	// Apply invalidates the cache so the next Status is fresh.
	if err := ts.Apply(ctx, Exposure{ProtoTCP, 2201, 2201, false}); err != nil {
		t.Fatal(err)
	}
	before := countStatus()
	ts.Status(ctx)
	if countStatus() != before+1 {
		t.Fatal("Status after Apply should query tailscale again")
	}
}

func TestInvalidateStatus(t *testing.T) {
	f := &fakeCLI{status: fixture(t, "status_no_https.json"), ports: map[int]bool{}}
	ts := NewTailscaleWith("tailscale", f.run)
	ts.statusTTL = time.Hour
	st, _ := ts.Status(context.Background())
	if st.HTTPSEnabled {
		t.Fatal("fixture should have HTTPS off")
	}
	f.mu.Lock()
	f.status = fixture(t, "status_ready.json") // user enables HTTPS in the admin console
	f.mu.Unlock()
	if st, _ := ts.Status(context.Background()); st.HTTPSEnabled {
		t.Fatal("expected cached (stale) status before invalidation")
	}
	ts.InvalidateStatus()
	if st, _ := ts.Status(context.Background()); !st.HTTPSEnabled {
		t.Fatal("Check again must see the new status")
	}
}
