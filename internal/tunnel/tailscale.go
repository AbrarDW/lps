package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abrardw/lps/internal/platform"
)

// Admin pages shown to the user when a tailnet feature must be switched on.
const (
	TailscaleHTTPSURL    = "https://login.tailscale.com/admin/dns"
	TailscaleFunnelURL   = "https://tailscale.com/kb/1223/funnel"
	TailscaleDownloadURL = "https://tailscale.com/download"

	capHTTPS       = "https"
	capFunnel      = "funnel"
	capFunnelPorts = "https://tailscale.com/cap/funnel-ports"
)

// Runner executes a command and returns its stdout and stderr.
type Runner func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	platform.HideConsole(cmd) // no flashing console window when run from lps.exe
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// Tailscale drives the locally installed Tailscale app through its CLI
// (`tailscale serve` / `tailscale funnel`).
type Tailscale struct {
	run     Runner
	lookup  func() string // finds the CLI; "" when not installed
	timeout time.Duration

	mu  sync.Mutex
	bin string

	// Status is polled by several pages; cache it briefly so tailscale.exe
	// isn't started every few seconds. Apply/Remove invalidate the cache.
	statusTTL time.Duration
	cacheMu   sync.Mutex
	cached    *Status
	cachedAt  time.Time
}

// NewTailscale returns a provider using the real CLI.
func NewTailscale() *Tailscale {
	return &Tailscale{run: execRunner, lookup: findTailscale, timeout: 30 * time.Second, statusTTL: 10 * time.Second}
}

// NewTailscaleWith is for tests: a fixed binary name and a fake runner.
func NewTailscaleWith(bin string, run Runner) *Tailscale {
	return &Tailscale{run: run, lookup: func() string { return bin }, timeout: 5 * time.Second}
}

// Name implements Provider.
func (t *Tailscale) Name() string { return "tailscale" }

func findTailscale() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramW6432"} {
			if d := os.Getenv(env); d != "" {
				candidates = append(candidates, d+`\Tailscale\tailscale.exe`)
			}
		}
		candidates = append(candidates, `C:\Program Files\Tailscale\tailscale.exe`)
	case "darwin":
		candidates = append(candidates, "/Applications/Tailscale.app/Contents/MacOS/Tailscale")
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func (t *Tailscale) binary() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bin == "" {
		t.bin = t.lookup() // re-checked until found, so installing later works
	}
	return t.bin
}

func (t *Tailscale) cli(ctx context.Context, args ...string) (string, error) {
	bin := t.binary()
	if bin == "" {
		return "", ErrNotInstalled
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	out, errOut, err := t.run(ctx, bin, args...)
	combined := strings.TrimSpace(string(out) + "\n" + string(errOut))
	if ctx.Err() == context.DeadlineExceeded {
		// `tailscale serve` blocks while waiting for a feature to be enabled.
		if u := findURL(combined); u != "" {
			return combined, &NeedsActionError{Message: "Tailscale is waiting for you to approve this in your browser", URL: u}
		}
		return combined, fmt.Errorf("tailscale %s: timed out", strings.Join(args, " "))
	}
	if err != nil {
		if combined == "" {
			combined = err.Error()
		}
		return combined, fmt.Errorf("tailscale %s: %s", strings.Join(args, " "), firstLines(combined, 4))
	}
	return combined, nil
}

var urlRe = regexp.MustCompile(`https://login\.tailscale\.com/[^\s"']+`)

func findURL(s string) string { return urlRe.FindString(s) }

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

// statusJSON is the subset of `tailscale status --json` LPS uses.
type statusJSON struct {
	BackendState   string `json:"BackendState"`
	AuthURL        string `json:"AuthURL"`
	MagicDNSSuffix string `json:"MagicDNSSuffix"`
	CurrentTailnet *struct {
		Name           string `json:"Name"`
		MagicDNSSuffix string `json:"MagicDNSSuffix"`
	} `json:"CurrentTailnet"`
	Self *struct {
		DNSName      string                     `json:"DNSName"`
		HostName     string                     `json:"HostName"`
		Capabilities []string                   `json:"Capabilities"` // older clients
		CapMap       map[string]json.RawMessage `json:"CapMap"`
	} `json:"Self"`
}

// ParseStatus converts `tailscale status --json` output into a Status.
func ParseStatus(data []byte) (Status, error) {
	var sj statusJSON
	if err := json.Unmarshal(data, &sj); err != nil {
		return Status{}, fmt.Errorf("parse tailscale status: %w", err)
	}
	st := Status{Installed: true, State: sj.BackendState, Running: sj.BackendState == "Running"}
	if sj.CurrentTailnet != nil {
		st.Tailnet = sj.CurrentTailnet.Name
	}
	if sj.Self == nil {
		return st, nil
	}
	st.DNSName = strings.TrimSuffix(sj.Self.DNSName, ".")
	st.HostName = sj.Self.HostName
	caps := map[string]bool{}
	for _, c := range sj.Self.Capabilities {
		caps[c] = true
	}
	for c := range sj.Self.CapMap {
		caps[c] = true
	}
	st.HTTPSEnabled = caps[capHTTPS]
	st.FunnelEnabled = st.HTTPSEnabled && caps[capFunnel]
	for c := range caps {
		if !strings.HasPrefix(c, capFunnelPorts) {
			continue
		}
		u, err := url.Parse(c)
		if err != nil {
			continue
		}
		for _, p := range strings.Split(u.Query().Get("ports"), ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
				st.FunnelPorts = append(st.FunnelPorts, n)
			}
		}
	}
	if st.FunnelEnabled && len(st.FunnelPorts) == 0 {
		st.FunnelPorts = []int{443, 8443, 10000} // Tailscale's default Funnel ports
	}
	sort.Ints(st.FunnelPorts)
	return st, nil
}

// Status implements Provider. Results are cached for statusTTL.
func (t *Tailscale) Status(ctx context.Context) (Status, error) {
	t.cacheMu.Lock()
	if t.cached != nil && time.Since(t.cachedAt) < t.statusTTL {
		st := *t.cached
		t.cacheMu.Unlock()
		return st, nil
	}
	t.cacheMu.Unlock()
	st, err := t.fetchStatus(ctx)
	if err == nil {
		t.cacheMu.Lock()
		t.cached, t.cachedAt = &st, time.Now()
		t.cacheMu.Unlock()
	}
	return st, err
}

// InvalidateStatus drops the cached status so the next call asks Tailscale
// (used by the "Check again" button).
func (t *Tailscale) InvalidateStatus() { t.invalidateStatus() }

func (t *Tailscale) invalidateStatus() {
	t.cacheMu.Lock()
	t.cached = nil
	t.cacheMu.Unlock()
}

func (t *Tailscale) fetchStatus(ctx context.Context) (Status, error) {
	out, err := t.cli(ctx, "status", "--json", "--peers=false")
	if errors.Is(err, ErrNotInstalled) {
		return Status{}, nil
	}
	// `status` exits non-zero when logged out / stopped but still prints JSON.
	if i := strings.Index(out, "{"); i >= 0 {
		if st, perr := ParseStatus([]byte(out[i:])); perr == nil {
			return st, nil
		}
	}
	if err != nil {
		// Installed but the service is not running.
		return Status{Installed: true, State: "NoState"}, nil
	}
	return Status{Installed: true}, fmt.Errorf("unexpected tailscale status output: %s", firstLines(out, 2))
}

// ParseServePorts returns the ports in `tailscale serve status --json` output.
func ParseServePorts(data []byte) (map[int]bool, error) {
	data = bytes.TrimSpace(data)
	ports := map[int]bool{}
	if len(data) == 0 || string(data) == "{}" || string(data) == "null" {
		return ports, nil
	}
	var sc struct {
		TCP map[string]json.RawMessage `json:"TCP"`
	}
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse tailscale serve status: %w", err)
	}
	for k := range sc.TCP {
		if n, err := strconv.Atoi(k); err == nil {
			ports[n] = true
		}
	}
	return ports, nil
}

// ParseServeConfig reads `tailscale serve status --json` into the exposures
// it describes, by remote port. Entries LPS can't describe as an Exposure
// (paths other than /, other targets) are returned with Proto "" and
// Target set, so callers can still report them.
func ParseServeConfig(data []byte) (map[int]ServeEntry, error) {
	data = bytes.TrimSpace(data)
	out := map[int]ServeEntry{}
	if len(data) == 0 || string(data) == "{}" || string(data) == "null" {
		return out, nil
	}
	var sc struct {
		TCP map[string]struct {
			HTTPS      bool   `json:"HTTPS"`
			HTTP       bool   `json:"HTTP"`
			TCPForward string `json:"TCPForward"`
		} `json:"TCP"`
		Web map[string]struct {
			Handlers map[string]struct {
				Proxy string `json:"Proxy"`
				Path  string `json:"Path"`
				Text  string `json:"Text"`
			} `json:"Handlers"`
		} `json:"Web"`
		AllowFunnel map[string]bool `json:"AllowFunnel"`
	}
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse tailscale serve status: %w", err)
	}
	localPort := func(target, scheme string) int {
		rest, ok := strings.CutPrefix(target, scheme)
		if !ok {
			return 0
		}
		host, port, err := net.SplitHostPort(strings.TrimSuffix(rest, "/"))
		if err != nil || (host != "127.0.0.1" && host != "localhost") {
			return 0
		}
		n, _ := strconv.Atoi(port)
		return n
	}
	for k, v := range sc.TCP {
		port, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		e := ServeEntry{}
		switch {
		case v.TCPForward != "":
			e.Target = v.TCPForward
			if n := localPort("tcp://"+v.TCPForward, "tcp://"); n > 0 {
				e.Exposure = Exposure{Proto: ProtoTCP, Port: port, LocalPort: n}
			}
		case v.HTTPS:
			for host, web := range sc.Web {
				if !strings.HasSuffix(host, ":"+k) {
					continue
				}
				if len(web.Handlers) == 1 {
					if h, ok := web.Handlers["/"]; ok && h.Proxy != "" {
						e.Target = h.Proxy
						if n := localPort(h.Proxy, "http://"); n > 0 {
							e.Exposure = Exposure{Proto: ProtoHTTPS, Port: port, LocalPort: n, Public: sc.AllowFunnel[host]}
						}
					}
				}
				if e.Target == "" {
					for path, h := range web.Handlers {
						e.Target = strings.TrimSpace(path + " " + h.Proxy + h.Path + h.Text)
						break
					}
				}
			}
		default:
			e.Target = "another kind of setting"
		}
		out[port] = e
	}
	return out, nil
}

// ServeEntry is one port of the Tailscale serve configuration.
type ServeEntry struct {
	Exposure        // Proto is "" when the entry isn't a plain LPS-style exposure
	Target   string `json:"target"` // what it points to, for messages
}

// Configured returns the serve configuration by remote port.
func (t *Tailscale) Configured(ctx context.Context) (map[int]ServeEntry, error) {
	out, err := t.cli(ctx, "serve", "status", "--json")
	if err != nil {
		return nil, err
	}
	if i := strings.Index(out, "{"); i >= 0 {
		out = out[i:]
	}
	return ParseServeConfig([]byte(out))
}

// UsedPorts implements Provider.
func (t *Tailscale) UsedPorts(ctx context.Context) (map[int]bool, error) {
	out, err := t.cli(ctx, "serve", "status", "--json")
	if err != nil {
		return nil, err
	}
	if i := strings.Index(out, "{"); i >= 0 {
		out = out[i:]
	}
	return ParseServePorts([]byte(out))
}

// ApplyArgs returns the CLI arguments that create e.
func ApplyArgs(e Exposure) []string {
	target := fmt.Sprintf("http://127.0.0.1:%d", e.LocalPort)
	if e.Proto == ProtoTCP {
		return []string{"serve", "--bg", "--yes", fmt.Sprintf("--tcp=%d", e.Port), fmt.Sprintf("tcp://127.0.0.1:%d", e.LocalPort)}
	}
	cmd := "serve"
	if e.Public {
		cmd = "funnel"
	}
	return []string{cmd, "--bg", "--yes", fmt.Sprintf("--https=%d", e.Port), target}
}

// RemoveArgs returns the CLI arguments that delete e.
func RemoveArgs(e Exposure) []string {
	if e.Proto == ProtoTCP {
		return []string{"serve", "--yes", fmt.Sprintf("--tcp=%d", e.Port), "off"}
	}
	cmd := "serve"
	if e.Public {
		cmd = "funnel"
	}
	return []string{cmd, "--yes", fmt.Sprintf("--https=%d", e.Port), "off"}
}

// CheckReady reports what the user must do before e can be applied.
func CheckReady(st Status, e Exposure) error {
	switch {
	case !st.Installed:
		return &NeedsActionError{Message: "Install the Tailscale app on this PC and sign in", URL: TailscaleDownloadURL}
	case !st.Running:
		return &NeedsActionError{Message: "Open the Tailscale app on this PC and sign in (state: " + st.State + ")"}
	case e.Proto == ProtoHTTPS && !st.HTTPSEnabled:
		return &NeedsActionError{Message: "Enable HTTPS certificates for your tailnet (Admin console > DNS > HTTPS Certificates)", URL: TailscaleHTTPSURL}
	case e.Public && !st.FunnelEnabled:
		return &NeedsActionError{Message: "Allow Tailscale Funnel for this PC to create public links", URL: TailscaleFunnelURL}
	case e.Public && !containsInt(st.FunnelPorts, e.Port):
		return fmt.Errorf("port %d is not allowed for Funnel (allowed: %v)", e.Port, st.FunnelPorts)
	}
	return nil
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// Apply implements Provider.
func (t *Tailscale) Apply(ctx context.Context, e Exposure) error {
	defer t.invalidateStatus()
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := CheckReady(st, e); err != nil {
		return err
	}
	out, err := t.cli(ctx, ApplyArgs(e)...)
	if err != nil {
		return err
	}
	// serve may print an enable URL and exit 0 without applying: verify.
	used, err := t.UsedPorts(ctx)
	if err != nil {
		return err
	}
	if !used[e.Port] {
		if u := findURL(out); u != "" {
			return &NeedsActionError{Message: "Tailscale needs you to approve this feature in your browser", URL: u}
		}
		return fmt.Errorf("tailscale did not apply %s: %s", e, firstLines(out, 3))
	}
	return nil
}

// Remove implements Provider.
func (t *Tailscale) Remove(ctx context.Context, e Exposure) error {
	defer t.invalidateStatus()
	_, err := t.cli(ctx, RemoveArgs(e)...)
	if err == nil || errors.Is(err, ErrNotInstalled) {
		return nil
	}
	// Removing something already gone fails; treat that as success.
	used, uerr := t.UsedPorts(ctx)
	if uerr == nil && !used[e.Port] {
		return nil
	}
	return err
}
