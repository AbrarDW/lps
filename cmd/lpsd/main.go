// Command lpsd is the LPS daemon: it turns this PC into a host for Linux
// VPSes (Ubuntu containers with SSH) and app deployments, managed from a web dashboard.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/abrardw/lps/internal/app"
	"github.com/abrardw/lps/internal/platform"
	"github.com/abrardw/lps/internal/store"
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
	case "disable-2fa":
		if err := disable2FA(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lpsd:", err)
			os.Exit(1)
		}
	case "autostart":
		if err := autostart(os.Args[2:]); err != nil {
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
  lpsd disable-2fa     turn off two-factor sign-in (if you lost your phone
                       and your recovery codes); run it on this PC
  lpsd autostart [on|off]
                       show, or turn on or off, starting LPS when you sign
                       in (needs the installed package or Windows app)
  lpsd version         print the version

Run "lpsd serve -h" for flags.
`)
}

// disable2FA turns two-factor sign-in off for an account. Being able to run
// it on this PC (with access to LPS's data folder) is the proof of ownership.
func disable2FA(args []string) error {
	fs := flag.NewFlagSet("disable-2fa", flag.ExitOnError)
	dataDir := fs.String("data", platform.DefaultDataDir(), "LPS data directory")
	user := fs.String("user", "", "account name (default: the only account)")
	fs.Parse(args)

	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(*dataDir, "lps.db"))
	if err != nil {
		return fmt.Errorf("open database in %s: %w", *dataDir, err)
	}
	defer st.Close()
	name := *user
	if name == "" {
		n, err := st.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("there are %d accounts; pass -user NAME", n)
		}
		names, err := st.Usernames(ctx)
		if err != nil {
			return err
		}
		name = names[0]
	}
	u, err := st.UserByUsername(ctx, name)
	if err != nil {
		return fmt.Errorf("account %q: %w", name, err)
	}
	if !u.TwoFactor {
		fmt.Printf("Two-factor sign-in is already off for %s.\n", u.Username)
		return nil
	}
	if err := st.DisableTOTP(ctx, u.ID); err != nil {
		return err
	}
	_ = st.AddEvent(ctx, &store.Event{Level: store.LevelWarning, Kind: "auth.2fa_off", Subject: "auth",
		Message: fmt.Sprintf("Two-factor sign-in turned off for %s from this PC (lpsd disable-2fa)", u.Username)})
	fmt.Printf("Two-factor sign-in is now off for %s. Sign in with your password, then turn it on again under Account.\n", u.Username)
	return nil
}

// autostart shows or changes whether LPS starts at sign-in, the same switch
// as on the dashboard. The macOS package's installer uses it to set up the
// launchd agent as the signed-in user.
func autostart(args []string) error {
	a := platform.NewAutostart()
	if !a.Supported() {
		return errors.New("starting at sign-in needs LPS installed from its package (or the Windows installer)")
	}
	switch {
	case len(args) == 0:
	case len(args) == 1 && (args[0] == "on" || args[0] == "off"):
		if err := a.Set(args[0] == "on"); err != nil {
			return err
		}
	default:
		return errors.New("usage: lpsd autostart [on|off]")
	}
	on, err := a.Enabled()
	if err != nil {
		return err
	}
	state := "off"
	if on {
		state = "on"
	}
	fmt.Println("Start LPS at sign-in:", state)
	return nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", app.DefaultAddr, "dashboard listen address (keep on localhost; use remote access to reach it from elsewhere)")
	dataDir := fs.String("data", platform.DefaultDataDir(), "directory for the database and state")
	bindIP := fs.String("bind-ip", "127.0.0.1", "host IP that VPS SSH and app ports are published on (0.0.0.0 = whole LAN)")
	secure := fs.Bool("secure-cookies", false, "mark session cookies Secure (set when the dashboard is reached over HTTPS)")
	noRemote := fs.Bool("no-remote", false, "disable remote access (Tailscale) integration")
	noUpdate := fs.Bool("no-update-check", false, "never check GitHub for new LPS releases")
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, app.Options{
		Addr: *addr, DataDir: *dataDir, BindIP: *bindIP, SecureCookies: *secure, NoRemote: *noRemote, NoUpdateCheck: *noUpdate,
		Version: version,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
}
