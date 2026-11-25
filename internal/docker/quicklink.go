package docker

// Quick links: a public https://<random>.trycloudflare.com address for an
// app, made by running Cloudflare's cloudflared ("Quick Tunnel", no account)
// in a small container on lps-net that forwards to the app by container
// name. Nothing is published on this PC: cloudflared connects out to
// Cloudflare.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// QuickLinkImage is the cloudflared image (pinned; LPS_CLOUDFLARED_IMAGE overrides it).
const QuickLinkImage = "cloudflare/cloudflared:2026.10.0"

func quickLinkImage() string {
	if v := strings.TrimSpace(os.Getenv("LPS_CLOUDFLARED_IMAGE")); v != "" {
		return v
	}
	return QuickLinkImage
}

// QuickLinkContainerName is the cloudflared container of an app.
func QuickLinkContainerName(app string) string { return "lps-cf-" + app }

const kindQuickLink = "quicklink"

// QuickLinkSpec describes an app's quick link.
type QuickLinkSpec struct {
	App    string // app name
	Target string // container serving the app (attached to lps-net)
	Port   int    // port the app listens on inside Target
}

func (s QuickLinkSpec) origin() string { return fmt.Sprintf("http://%s:%d", s.Target, s.Port) }

// Quick link states.
const (
	QuickStarting = "starting" // running, no address yet
	QuickActive   = "active"   // address assigned
	QuickError    = "error"    // stopped or failed
	QuickMissing  = "missing"  // no container
)

// QuickLink is the live state of an app's quick link.
type QuickLink struct {
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

var (
	quickURLRe = regexp.MustCompile(`https://([a-z0-9-]+)\.trycloudflare\.com\b`)
	logTimeRe  = regexp.MustCompile(`^\S+Z\s+(?:[A-Z]{3}\s+)?`)
)

// ParseQuickLinkLog finds the address cloudflared printed, or its last error.
func ParseQuickLinkLog(log string) (url, errMsg string) {
	for _, line := range strings.Split(log, "\n") {
		if m := quickURLRe.FindStringSubmatch(line); m != nil && m[1] != "api" {
			url = m[0]
		}
		l := strings.TrimSpace(line)
		// Docker's own timestamp, then cloudflared's.
		for i := 0; i < 2; i++ {
			l = logTimeRe.ReplaceAllString(l, "")
		}
		if strings.Contains(line, " ERR ") || strings.HasPrefix(l, "failed to") {
			errMsg = l
		}
	}
	return url, errMsg
}

// friendlyQuickErr explains the usual failures.
func friendlyQuickErr(msg string) string {
	switch {
	case strings.Contains(msg, "failed to request quick Tunnel") && (strings.Contains(msg, "no such host") || strings.Contains(msg, "dial tcp") || strings.Contains(msg, "timeout")):
		return "can't reach Cloudflare (api.trycloudflare.com) from this PC. Check the internet connection or firewall. " + msg
	case strings.Contains(msg, "429") || strings.Contains(strings.ToLower(msg), "too many"):
		return "Cloudflare is limiting new quick links from this PC right now; LPS keeps trying. " + msg
	}
	return msg
}

func (c *Client) ensureImage(ctx context.Context, ref string) error {
	if _, err := c.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	res, err := c.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("download %s: %w", ref, err)
	}
	defer res.Close()
	if err := res.Wait(ctx); err != nil {
		return fmt.Errorf("download %s: %w", ref, err)
	}
	return nil
}

// EnsureQuickLink implements Engine: it makes sure the app's cloudflared
// container exists, points at spec's origin and runs. A running container
// is left alone, so the address stays the same.
func (c *Client) EnsureQuickLink(ctx context.Context, spec QuickLinkSpec) error {
	if err := c.EnsureNetwork(ctx, spec.Target); err != nil {
		return fmt.Errorf("connect %s to %s: %w", spec.Target, NetworkName, err)
	}
	name := QuickLinkContainerName(spec.App)
	insp, err := c.cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	switch {
	case err == nil && insp.Container.Config != nil && insp.Container.Config.Labels["lps.origin"] == spec.origin():
		if insp.Container.State != nil && (insp.Container.State.Running || insp.Container.State.Restarting) {
			return nil
		}
		return c.Start(ctx, insp.Container.ID)
	case err == nil:
		// The app's port changed: start over (new address).
		if err := c.Remove(ctx, insp.Container.ID); err != nil {
			return err
		}
	case !cerrdefs.IsNotFound(err):
		return err
	}
	img := quickLinkImage()
	if err := c.ensureImage(ctx, img); err != nil {
		return err
	}
	lbls := labels(kindQuickLink, spec.App)
	lbls["lps.origin"] = spec.origin()
	res, err := c.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: img,
			Cmd: []string{"tunnel", "--no-autoupdate", "--metrics", "127.0.0.1:0",
				"--url", spec.origin()},
			Labels: lbls,
		},
		HostConfig: &container.HostConfig{
			NetworkMode:   container.NetworkMode(NetworkName),
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Resources:     resources(0.5, 128),
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{NetworkName: {}},
		},
	})
	if err != nil {
		return err
	}
	return c.Start(ctx, res.ID)
}

// QuickLinkStatus implements Engine. It reads the address from the
// container's output since it last started.
func (c *Client) QuickLinkStatus(ctx context.Context, app string) (QuickLink, error) {
	insp, err := c.cli.ContainerInspect(ctx, QuickLinkContainerName(app), client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return QuickLink{State: QuickMissing}, nil
	} else if err != nil {
		return QuickLink{}, err
	}
	st := insp.Container.State
	opts := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: "400"}
	if st != nil && st.Running && st.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, st.StartedAt); err == nil {
			opts.Since = t.Add(-time.Second).Format(time.RFC3339Nano)
		}
	}
	rc, err := c.cli.ContainerLogs(ctx, insp.Container.ID, opts)
	if err != nil {
		return QuickLink{}, err
	}
	defer rc.Close()
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, rc); err != nil {
		return QuickLink{}, err
	}
	url, errMsg := ParseQuickLinkLog(out.String())
	switch {
	case st != nil && st.Running && url != "":
		return QuickLink{State: QuickActive, URL: url}, nil
	case st != nil && st.Running:
		return QuickLink{State: QuickStarting, Error: friendlyQuickErr(errMsg)}, nil
	default:
		// Only a failure to get an address explains the stop; other errors
		// are about requests the link handled earlier.
		if !strings.HasPrefix(errMsg, "failed to") {
			errMsg = "the link stopped; LPS starts it again within a minute"
			if st != nil && st.ExitCode != 0 {
				errMsg = fmt.Sprintf("the link stopped (cloudflared exit code %d); LPS starts it again within a minute", st.ExitCode)
			}
		}
		return QuickLink{State: QuickError, Error: friendlyQuickErr(errMsg)}, nil
	}
}

// RemoveQuickLink implements Engine; a missing link is not an error.
func (c *Client) RemoveQuickLink(ctx context.Context, app string) error {
	return c.Remove(ctx, QuickLinkContainerName(app))
}

// QuickLinkApps implements Engine: the apps that have a cloudflared container.
func (c *Client) QuickLinkApps(ctx context.Context) ([]string, error) {
	res, err := c.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", "lps.kind="+kindQuickLink),
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ct := range res.Items {
		if n := ct.Labels["lps.name"]; n != "" {
			out = append(out, n)
		}
	}
	return out, nil
}
