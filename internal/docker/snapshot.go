package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/abrardw/lps/images"
)

// SnapshotImage is the image a VPS snapshot's system files are saved as.
// The creation time keeps names unique even if LPS's database is reset and
// snapshot ids start again at 1 while old images still exist.
func SnapshotImage(vps string, id int64, created time.Time) string {
	return fmt.Sprintf("lps-snap-%s:%d-%d", vps, id, created.Unix())
}

// SnapshotVolume holds a copy of a VPS's /home for a snapshot.
func SnapshotVolume(id int64, created time.Time) string {
	return fmt.Sprintf("lps-snap-%d-%d-home", id, created.Unix())
}

// Commit implements Engine: it saves a container's filesystem (not its
// volumes) as an image. The container is paused for the moment it takes.
func (c *Client) Commit(ctx context.Context, id, ref, comment string) error {
	_, err := c.cli.ContainerCommit(ctx, id, client.ContainerCommitOptions{Reference: ref, Comment: comment, Author: "LPS"})
	return err
}

// TagImage implements Engine.
func (c *Client) TagImage(ctx context.Context, src, dst string) error {
	_, err := c.cli.ImageTag(ctx, client.ImageTagOptions{Source: src, Target: dst})
	return err
}

// ImageSize implements Engine.
func (c *Client) ImageSize(ctx context.Context, ref string) (int64, error) {
	res, err := c.cli.ImageInspect(ctx, ref)
	if err != nil {
		return 0, err
	}
	return res.Size, nil
}

// CopyVolume implements Engine: it makes volume to an exact copy of from
// (creating it if needed, replacing what was in it) and returns its size.
func (c *Client) CopyVolume(ctx context.Context, from, to string, lbls map[string]string) (int64, error) {
	if _, err := c.cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: to, Labels: lbls}); err != nil {
		return 0, fmt.Errorf("create volume %s: %w", to, err)
	}
	script := `set -e
find /to -mindepth 1 -delete
cp -a /from/. /to/
du -sb /to | cut -f1`
	var out bytes.Buffer
	code, err := c.runHelper(ctx, []mount.Mount{
		{Type: mount.TypeVolume, Source: from, Target: "/from", ReadOnly: true},
		{Type: mount.TypeVolume, Source: to, Target: "/to"},
	}, []string{"sh", "-c", script}, &out)
	if err != nil {
		return 0, err
	}
	if code != 0 {
		return 0, fmt.Errorf("copy %s to %s failed: %s", from, to, strings.TrimSpace(out.String()))
	}
	lines := strings.Fields(out.String())
	if len(lines) == 0 {
		return 0, nil
	}
	n, _ := strconv.ParseInt(lines[len(lines)-1], 10, 64)
	return n, nil
}

// runHelper runs a short-lived container from the VPS image (always present
// once a VPS exists) with the given volumes, without network, and returns
// its exit code and output.
func (c *Client) runHelper(ctx context.Context, mounts []mount.Mount, cmd []string, out io.Writer) (int, error) {
	res, err := c.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:      images.UbuntuVPSTag,
			Entrypoint: cmd[:1],
			Cmd:        cmd[1:],
			Labels:     map[string]string{"lps.managed": "true", "lps.kind": "helper"},
		},
		HostConfig: &container.HostConfig{Mounts: mounts, NetworkMode: "none"},
	})
	if err != nil {
		return -1, err
	}
	defer c.cli.ContainerRemove(context.WithoutCancel(ctx), res.ID, client.ContainerRemoveOptions{Force: true})
	if _, err := c.cli.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		return -1, err
	}
	wait := c.cli.ContainerWait(ctx, res.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	var code int
	select {
	case r := <-wait.Result:
		code = int(r.StatusCode)
	case err := <-wait.Error:
		return -1, err
	}
	logs, err := c.cli.ContainerLogs(ctx, res.ID, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err == nil {
		defer logs.Close()
		_, _ = stdcopy.StdCopy(out, out, logs)
	}
	return code, nil
}

// RemoveSnapshotImage implements Engine.
func (c *Client) RemoveSnapshotImage(ctx context.Context, ref string) error {
	_, err := c.cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{Force: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}
