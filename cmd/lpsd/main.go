// Command lpsd is the LPS daemon: it turns this PC into a host for Linux
// VPSes (Ubuntu containers with SSH) and app deployments, managed from a web dashboard.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/abrardw/lps/internal/app"
	"github.com/abrardw/lps/internal/platform"
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
	addr := fs.String("addr", app.DefaultAddr, "dashboard listen address (keep on localhost; use remote access to reach it from elsewhere)")
	dataDir := fs.String("data", platform.DefaultDataDir(), "directory for the database and state")
	bindIP := fs.String("bind-ip", "127.0.0.1", "host IP that VPS SSH and app ports are published on (0.0.0.0 = whole LAN)")
	secure := fs.Bool("secure-cookies", false, "mark session cookies Secure (set when the dashboard is reached over HTTPS)")
	noRemote := fs.Bool("no-remote", false, "disable remote access (Tailscale) integration")
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, app.Options{
		Addr: *addr, DataDir: *dataDir, BindIP: *bindIP, SecureCookies: *secure, NoRemote: *noRemote,
		Version: version,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
}
