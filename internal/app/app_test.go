package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRunStartsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Addr: "127.0.0.1:0", DataDir: t.TempDir(), NoRemote: true, Version: "test",
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			OnReady: func(url string) { ready <- url },
		})
	}()
	var url string
	select {
	case url = <-ready:
	case err := <-done:
		t.Fatalf("Run exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
	if !Healthy(strings.TrimPrefix(url, "http://")) {
		t.Fatal("health check failed")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunReportsPortInUse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	go Run(ctx, Options{Addr: "127.0.0.1:0", DataDir: t.TempDir(), NoRemote: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OnReady: func(u string) { ready <- u }})
	url := <-ready
	err := Run(ctx, Options{Addr: strings.TrimPrefix(url, "http://"), DataDir: t.TempDir(), NoRemote: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("want listen error, got %v", err)
	}
}
