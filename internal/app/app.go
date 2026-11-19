// Package app wires the LPS server together; it is shared by the lpsd CLI
// and the lps desktop launcher.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/abrardw/lps/internal/api"
	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/platform"
	"github.com/abrardw/lps/internal/store"
	"github.com/abrardw/lps/internal/tunnel"
	"github.com/abrardw/lps/web"
)

// DefaultAddr is where the dashboard listens by default.
const DefaultAddr = "127.0.0.1:7070"

// Options configures Run.
type Options struct {
	Addr          string
	DataDir       string
	BindIP        string
	SecureCookies bool
	NoRemote      bool
	Version       string
	Logger        *slog.Logger
	// OnReady is called once the dashboard is accepting connections.
	OnReady func(url string)
}

// Run starts LPS and blocks until ctx is cancelled or the server fails.
func Run(ctx context.Context, o Options) error {
	if o.Addr == "" {
		o.Addr = DefaultAddr
	}
	if o.DataDir == "" {
		o.DataDir = platform.DefaultDataDir()
	}
	if o.BindIP == "" {
		o.BindIP = "127.0.0.1"
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return err
	}

	st, err := store.Open(ctx, filepath.Join(o.DataDir, "lps.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()
	if err := st.FailStaleDeployments(ctx); err != nil {
		return err
	}

	eng, err := docker.NewClient(o.BindIP)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	if v, err := eng.Ping(ctx); err != nil {
		logger.Warn("Docker engine not reachable; VPS and app features will fail until it is running",
			"err", err, "hint", platform.DockerHint())
	} else {
		logger.Info("connected to Docker", "version", v)
	}

	cfg := api.DefaultConfig()
	cfg.BindIP = o.BindIP
	cfg.SecureCookies = o.SecureCookies
	cfg.Version = o.Version
	cfg.DataDir = o.DataDir
	if _, p, err := net.SplitHostPort(o.Addr); err == nil {
		cfg.DashboardPort, _ = strconv.Atoi(p)
	}
	var tp tunnel.Provider
	if !o.NoRemote {
		tp = tunnel.NewTailscale()
	}
	srv := api.New(cfg, st, eng, tp, web.Dist(), logger)
	defer srv.Shutdown()

	// Listen first so "address already in use" is reported immediately.
	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", o.Addr, err)
	}
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	url := "http://" + ln.Addr().String()
	logger.Info("LPS dashboard listening", "url", url, "data", o.DataDir)
	if o.OnReady != nil {
		o.OnReady(url)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	return nil
}

// Healthy reports whether an LPS dashboard is already answering at addr.
func Healthy(addr string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	res, err := c.Get("http://" + addr + "/api/health")
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}
