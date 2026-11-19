package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/abrardw/lps/images"
	"github.com/abrardw/lps/internal/builder"
)

// Client implements Engine using the Docker Engine API.
type Client struct {
	cli *client.Client
	// BindIP is the host address published ports bind to. Default 127.0.0.1 so
	// nothing is exposed on the LAN unless the user opts in.
	BindIP netip.Addr
}

// NewClient connects using DOCKER_HOST etc., or the platform default
// (npipe on Windows, unix socket on Linux/macOS).
func NewClient(bindIP string) (*Client, error) {
	ip, err := netip.ParseAddr(bindIP)
	if err != nil {
		return nil, fmt.Errorf("bind ip: %w", err)
	}
	c, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Client{cli: c, BindIP: ip}, nil
}

func labels(kind, name string) map[string]string {
	return map[string]string{"lps.managed": "true", "lps.kind": kind, "lps.name": name}
}

// Ping implements Engine.
func (c *Client) Ping(ctx context.Context) (string, error) {
	v, err := c.cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// EnsureVPSImage implements Engine.
func (c *Client) EnsureVPSImage(ctx context.Context, log io.Writer) error {
	if _, err := c.cli.ImageInspect(ctx, images.UbuntuVPSTag); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return err
	}
	var buf bytes.Buffer
	if err := builder.TarFS(images.UbuntuVPS, "ubuntu-vps", &buf); err != nil {
		return err
	}
	return c.BuildImage(ctx, &buf, images.UbuntuVPSTag, "Dockerfile", log)
}

// BuildImage implements Engine.
func (c *Client) BuildImage(ctx context.Context, buildContext io.Reader, tag, dockerfile string, log io.Writer) error {
	res, err := c.cli.ImageBuild(ctx, buildContext, client.ImageBuildOptions{
		Tags:        []string{tag},
		Remove:      true,
		ForceRemove: true,
		Dockerfile:  dockerfile,
		Labels:      map[string]string{"lps.managed": "true"},
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return decodeBuildStream(res.Body, log)
}

// decodeBuildStream converts the daemon's JSON message stream into plain log text.
func decodeBuildStream(r io.Reader, log io.Writer) error {
	dec := json.NewDecoder(r)
	for {
		var msg struct {
			Stream      string `json:"stream"`
			Status      string `json:"status"`
			Error       string `json:"error"`
			ErrorDetail *struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		switch {
		case msg.Error != "":
			fmt.Fprintln(log, msg.Error)
			return errors.New(msg.Error)
		case msg.ErrorDetail != nil && msg.ErrorDetail.Message != "":
			fmt.Fprintln(log, msg.ErrorDetail.Message)
			return errors.New(msg.ErrorDetail.Message)
		case msg.Stream != "":
			io.WriteString(log, msg.Stream)
		case msg.Status != "":
			fmt.Fprintln(log, msg.Status)
		}
	}
}

func resources(cpus float64, memMB int) container.Resources {
	r := container.Resources{}
	if cpus > 0 {
		r.NanoCPUs = int64(cpus * 1e9)
	}
	if memMB > 0 {
		r.Memory = int64(memMB) * 1024 * 1024
	}
	return r
}

func (c *Client) portMap(containerPort, hostPort int) (network.PortSet, network.PortMap, error) {
	p, err := network.ParsePort(strconv.Itoa(containerPort) + "/tcp")
	if err != nil {
		return nil, nil, err
	}
	return network.PortSet{p: {}},
		network.PortMap{p: {{HostIP: c.BindIP, HostPort: strconv.Itoa(hostPort)}}}, nil
}

// CreateVPS implements Engine.
func (c *Client) CreateVPS(ctx context.Context, spec VPSSpec) (string, error) {
	exposed, bindings, err := c.portMap(22, spec.SSHPort)
	if err != nil {
		return "", err
	}
	vol := VPSVolumeName(spec.Name)
	if _, err := c.cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: vol, Labels: labels("vps", spec.Name)}); err != nil {
		return "", fmt.Errorf("create volume: %w", err)
	}
	res, err := c.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: VPSContainerName(spec.Name),
		Config: &container.Config{
			Image:        images.UbuntuVPSTag,
			Hostname:     spec.Name,
			ExposedPorts: exposed,
			Labels:       labels("vps", spec.Name),
		},
		HostConfig: &container.HostConfig{
			PortBindings:  bindings,
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Resources:     resources(spec.CPUs, spec.MemoryMB),
			Mounts:        []mount.Mount{{Type: mount.TypeVolume, Source: vol, Target: "/home"}},
			// Plain unprivileged container: no Docker socket, no extra capabilities.
		},
	})
	if err != nil {
		return "", err
	}
	if err := c.copyInitEnv(ctx, res.ID, spec); err != nil {
		_ = c.Remove(ctx, res.ID)
		return "", fmt.Errorf("provision: %w", err)
	}
	return res.ID, nil
}

// copyInitEnv writes /etc/lps/init.env into the created container; the
// entrypoint applies it on first boot and deletes it.
func (c *Client) copyInitEnv(ctx context.Context, id string, spec VPSSpec) error {
	// Values are either validated (user: [a-z_][a-z0-9_-]*) or base64, so
	// single-quoting is safe for the entrypoint's `. init.env`.
	b64 := base64.StdEncoding.EncodeToString
	var env strings.Builder
	fmt.Fprintf(&env, "LPS_USER='%s'\n", spec.User)
	if key := strings.TrimSpace(spec.PublicKey); key != "" {
		fmt.Fprintf(&env, "LPS_PUBKEY_B64='%s'\n", b64([]byte(key+"\n")))
	}
	if spec.Password != "" {
		fmt.Fprintf(&env, "LPS_PASSWORD_B64='%s'\n", b64([]byte(spec.Password)))
	}
	buf, err := singleFileTar("init.env", []byte(env.String()), 0o600)
	if err != nil {
		return err
	}
	_, err = c.cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: "/etc/lps", Content: buf})
	return err
}

// singleFileTar returns a tar archive holding one root-owned file.
func singleFileTar(name string, data []byte, mode int64) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(data); err != nil {
		return nil, err
	}
	return &buf, tw.Close()
}

// CreateApp implements Engine.
func (c *Client) CreateApp(ctx context.Context, spec AppSpec) (string, error) {
	exposed, bindings, err := c.portMap(spec.ContainerPort, spec.HostPort)
	if err != nil {
		return "", err
	}
	res, err := c.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: AppContainerName(spec.Name),
		Config: &container.Config{
			Image:        spec.Image,
			ExposedPorts: exposed,
			Labels:       labels("app", spec.Name),
		},
		HostConfig: &container.HostConfig{
			PortBindings:  bindings,
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Resources:     resources(spec.CPUs, spec.MemoryMB),
		},
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// Start implements Engine.
func (c *Client) Start(ctx context.Context, id string) error {
	_, err := c.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

// Stop implements Engine.
func (c *Client) Stop(ctx context.Context, id string) error {
	t := 10
	_, err := c.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &t})
	return err
}

// Remove implements Engine.
func (c *Client) Remove(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	_, err := c.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// RemoveVolume implements Engine.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	_, err := c.cli.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// State implements Engine.
func (c *Client) State(ctx context.Context, id string) (string, error) {
	if id == "" {
		return StateMissing, nil
	}
	res, err := c.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return StateMissing, nil
	}
	if err != nil {
		return "", err
	}
	if res.Container.State != nil && res.Container.State.Running {
		return StateRunning, nil
	}
	return StateStopped, nil
}

// Logs implements Engine.
func (c *Client) Logs(ctx context.Context, id string, tail int) (string, error) {
	rc, err := c.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(tail), Timestamps: true,
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, rc); err != nil {
		return "", err
	}
	return out.String(), nil
}
