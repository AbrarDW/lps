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
	"github.com/abrardw/lps/internal/update"
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
	// Quit stops the program around Run (the desktop app's tray). Setting it
	// lets LPS install updates: it starts the installer, then quits.
	Quit func()
	// NoUpdateCheck turns off checking GitHub for new releases.
	NoUpdateCheck bool
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
	if err := st.FailStaleSnapshots(ctx); err != nil {
		return err
	}
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
	cfg.Quit = o.Quit
	if !o.NoUpdateCheck {
		cfg.UpdateRepo = update.DefaultRepo
	}
	if _, p, err := net.SplitHostPort(o.Addr); err == nil {
		cfg.DashboardPort, _ = strconv.Atoi(p)
	}
	var tp tunnel.Provider
	if !o.NoRemote {
		tp = tunnel.NewTailscale()
	}
	// The apps gateway (https://<pc>.ts.net:8080/<app>/ over Tailscale) has its
	// own local port, next to the dashboard's if it is free.
	gwLn := listenGateway(o.Addr, cfg.DashboardPort, logger)
	if gwLn != nil {
		defer gwLn.Close()
		cfg.GatewayPort = gwLn.Addr().(*net.TCPAddr).Port
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
	var gwSrv *http.Server
	if gwLn != nil {
		gwSrv = &http.Server{Handler: srv.GatewayHandler(), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := gwSrv.Serve(gwLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Warn("apps gateway stopped", "err", err)
			}
		}()
		logger.Info("apps gateway listening", "url", "http://"+gwLn.Addr().String())
	}

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
	if gwSrv != nil {
		_ = gwSrv.Shutdown(shutdownCtx)
	}
	return nil
}

// listenGateway opens the apps gateway's local listener: the port after the
// dashboard's if free, else any free port. It returns nil if neither works
// (then apps keep their port-based addresses only).
func listenGateway(dashAddr string, dashPort int, logger *slog.Logger) net.Listener {
	host, _, err := net.SplitHostPort(dashAddr)
	if err != nil || dashPort == 0 {
		return nil
	}
	if ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(dashPort+1))); err == nil {
		return ln
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		logger.Warn("apps gateway disabled", "err", err)
		return nil
	}
	return ln
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
