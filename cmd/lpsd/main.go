// Command lpsd is the LPS daemon: it turns this PC into a host for Linux
// VPSes (Ubuntu containers with SSH) and app deployments, managed from a web dashboard.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/abrardw/lps/internal/api"
	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/platform"
	"github.com/abrardw/lps/internal/store"
	"github.com/abrardw/lps/web"
)

var version = "dev" // set with -ldflags "-X main.version=..."

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lpsd:", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Println("lpsd", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `lpsd - LPS: run Linux VPSes and web apps on your home PC

Usage:
  lpsd serve [flags]   start the dashboard and API
  lpsd version         print the version

Run "lpsd serve -h" for flags.
`)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:7070", "dashboard listen address (keep on localhost; expose remotely via a tunnel)")
	dataDir := fs.String("data", platform.DefaultDataDir(), "directory for the database and state")
	bindIP := fs.String("bind-ip", "127.0.0.1", "host IP that VPS SSH and app ports are published on (0.0.0.0 = whole LAN)")
	secure := fs.Bool("secure-cookies", false, "mark session cookies Secure (set when the dashboard is reached over HTTPS)")
	fs.Parse(args)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, filepath.Join(*dataDir, "lps.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()
	if err := st.FailStaleDeployments(ctx); err != nil {
		return err
	}

	eng, err := docker.NewClient(*bindIP)
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
	cfg.BindIP = *bindIP
	cfg.SecureCookies = *secure
	cfg.Version = version
	srv := api.New(cfg, st, eng, web.Dist(), logger)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("LPS dashboard listening", "url", "http://"+*addr, "data", *dataDir)
		errCh <- httpSrv.ListenAndServe()
	}()

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
	srv.Shutdown()
	return nil
}
