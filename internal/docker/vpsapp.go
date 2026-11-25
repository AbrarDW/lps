package docker

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"strconv"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/abrardw/lps/images"
)

// NetworkName is the Docker network VPSes and their app relays share, so a
// relay can reach a VPS by container name whatever IP it gets.
const NetworkName = "lps-net"

// AppHelperPath is where lpsd installs the lps-app helper inside a VPS.
const AppHelperPath = "/usr/local/lib/lps/lps-app"

// AppHelper is the shell script that starts, stops and inspects an app's
// process inside a VPS (see lps-app.sh).
//
//go:embed lps-app.sh
var AppHelper []byte

// relayScript forwards TCP connections: listen on ARGV[0], connect to
// ARGV[1]:ARGV[2]. It runs on the perl that ships with every Ubuntu image, so
// the relay reuses the VPS image and needs no download or build.
const relayScript = `use strict; use IO::Socket::INET; use IO::Select;
$| = 1; $SIG{CHLD} = 'IGNORE'; $SIG{PIPE} = 'IGNORE';
my ($lport, $host, $port) = @ARGV;
my $l = IO::Socket::INET->new(LocalAddr => '0.0.0.0', LocalPort => $lport, Listen => 128, ReuseAddr => 1)
  or die "listen on $lport: $!\n";
print "relay :$lport -> $host:$port\n";
while (1) {
  my $c = $l->accept or next;
  my $pid = fork;
  if (!defined $pid || $pid) { close $c; next; }
  close $l;
  my $u = IO::Socket::INET->new(PeerAddr => $host, PeerPort => $port, Timeout => 10) or exit 1;
  my $sel = IO::Select->new($c, $u);
  my $buf;
  while ($sel->count) {
    for my $h ($sel->can_read) {
      my $o = $h == $c ? $u : $c;
      my $n = sysread($h, $buf, 65536);
      if (!$n) { $sel->remove($h); shutdown($o, 1); next; }
      my $off = 0;
      while ($off < $n) { my $w = syswrite($o, $buf, $n - $off, $off); exit 0 unless defined $w; $off += $w; }
    }
  }
  exit 0;
}`

// RelaySpec describes the relay that publishes an app running inside a VPS.
type RelaySpec struct {
	App      string // app name
	VPS      string // VPS name
	Port     int    // port the app listens on inside the VPS
	HostPort int    // host port to publish
}

// RelayContainerName is the container that publishes a VPS app's port.
func RelayContainerName(app string) string { return "lps-relay-" + app }

// Exec implements Engine.
func (c *Client) Exec(ctx context.Context, id, user, dir string, cmd, env []string, out io.Writer) (int, error) {
	if out == nil {
		out = io.Discard
	}
	ex, err := c.cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		User: user, AttachStdout: true, AttachStderr: true, WorkingDir: dir, Env: env, Cmd: cmd,
	})
	if err != nil {
		return -1, err
	}
	res, err := c.cli.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{})
	if err != nil {
		return -1, err
	}
	defer res.Close()
	if _, err := stdcopy.StdCopy(out, out, res.Reader); err != nil {
		return -1, err
	}
	insp, err := c.cli.ExecInspect(ctx, ex.ID, client.ExecInspectOptions{})
	if err != nil {
		return -1, err
	}
	return insp.ExitCode, nil
}

// CopyTo implements Engine.
func (c *Client) CopyTo(ctx context.Context, id, dir string, content io.Reader) error {
	_, err := c.cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: dir, Content: content})
	return err
}

// EnsureNetwork implements Engine: it creates lps-net if needed and attaches
// the VPS container to it (VPSes created before LPS used the network are
// attached here, while running).
func (c *Client) EnsureNetwork(ctx context.Context, vpsContainer string) error {
	if err := c.ensureNetwork(ctx); err != nil {
		return err
	}
	insp, err := c.cli.ContainerInspect(ctx, vpsContainer, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	if ns := insp.Container.NetworkSettings; ns != nil {
		if _, ok := ns.Networks[NetworkName]; ok {
			return nil
		}
	}
	_, err = c.cli.NetworkConnect(ctx, NetworkName, client.NetworkConnectOptions{Container: vpsContainer})
	return err
}

// ensureNetwork creates lps-net if it is missing.
func (c *Client) ensureNetwork(ctx context.Context) error {
	if _, err := c.cli.NetworkInspect(ctx, NetworkName, client.NetworkInspectOptions{}); cerrdefs.IsNotFound(err) {
		_, err = c.cli.NetworkCreate(ctx, NetworkName, client.NetworkCreateOptions{
			Driver: "bridge", Labels: map[string]string{"lps.managed": "true"},
		})
		if err != nil && !cerrdefs.IsConflict(err) {
			return fmt.Errorf("create network: %w", err)
		}
	} else if err != nil {
		return err
	}
	return nil
}

// CreateRelay implements Engine. The relay runs the VPS image (already
// present) with a perl TCP forwarder instead of sshd.
func (c *Client) CreateRelay(ctx context.Context, spec RelaySpec) (string, error) {
	exposed, bindings, err := c.portMap(spec.Port, spec.HostPort)
	if err != nil {
		return "", err
	}
	res, err := c.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: RelayContainerName(spec.App),
		Config: &container.Config{
			Image:        images.UbuntuVPSTag,
			Entrypoint:   []string{"/usr/bin/tini", "--", "perl", "-e", relayScript},
			Cmd:          []string{strconv.Itoa(spec.Port), VPSContainerName(spec.VPS), strconv.Itoa(spec.Port)},
			ExposedPorts: exposed,
			Labels:       labels("relay", spec.App),
		},
		HostConfig: &container.HostConfig{
			PortBindings:  bindings,
			NetworkMode:   container.NetworkMode(NetworkName),
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Resources:     resources(0.25, 64),
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{NetworkName: {}},
		},
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// InstallAppHelper implements Engine: it (re)installs lps-app in a running VPS.
func (c *Client) InstallAppHelper(ctx context.Context, id string) error {
	var out bytes.Buffer
	code, err := c.Exec(ctx, id, "root", "/", []string{"mkdir", "-p", "/usr/local/lib/lps"}, nil, &out)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("mkdir: %s", out.String())
	}
	buf, err := singleFileTar("lps-app", AppHelper, 0o755)
	if err != nil {
		return err
	}
	return c.CopyTo(ctx, id, "/usr/local/lib/lps", buf)
}
