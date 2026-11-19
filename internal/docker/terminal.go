package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
)

// execTerminal is a TTY exec session attached over a hijacked connection.
type execTerminal struct {
	cli    *client.Client
	execID string
	resp   client.HijackedResponse
}

func (t *execTerminal) Read(p []byte) (int, error)  { return t.resp.Reader.Read(p) }
func (t *execTerminal) Write(p []byte) (int, error) { return t.resp.Conn.Write(p) }
func (t *execTerminal) Close() error                { t.resp.Close(); return nil }

func (t *execTerminal) Resize(ctx context.Context, cols, rows uint) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	_, err := t.cli.ExecResize(ctx, t.execID, client.ExecResizeOptions{Width: cols, Height: rows})
	return err
}

// OpenTerminal implements Engine. The shell runs as the VPS's SSH user in
// their home directory, exactly as an SSH login would.
func (c *Client) OpenTerminal(ctx context.Context, containerID, user string, cols, rows uint) (Terminal, error) {
	if cols == 0 || rows == 0 {
		cols, rows = 80, 24
	}
	size := client.ConsoleSize{Width: cols, Height: rows}
	ex, err := c.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		User:         user,
		TTY:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		ConsoleSize:  size,
		WorkingDir:   "/home/" + user,
		Env:          []string{"TERM=xterm-256color", "LANG=C.UTF-8", "COLORTERM=truecolor"},
		Cmd:          []string{"/bin/bash", "-l"},
	})
	if err != nil {
		return nil, fmt.Errorf("create shell: %w", err)
	}
	res, err := c.cli.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{TTY: true, ConsoleSize: size})
	if err != nil {
		return nil, fmt.Errorf("attach shell: %w", err)
	}
	return &execTerminal{cli: c.cli, execID: ex.ID, resp: res.HijackedResponse}, nil
}
