// Command psp-sandbox runs a fake payment provider for tests.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/app"
	"github.com/ianfoxdev/psp-sandbox/internal/config"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(os.Getenv("PSP_ADDR")))
	}
	os.Exit(run())
}

// healthcheck asks the running server for /healthz. The image is distroless,
// with no shell or curl, so a compose or Docker healthcheck calls the binary:
// ["CMD", "/psp-sandbox", "healthcheck"].
func healthcheck(addr string) int {
	if addr == "" {
		addr = ":8090"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: /healthz answered", resp.StatusCode)
		return 1
	}
	return 0
}

func run() int {
	cfg, err := config.FromEnv()
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		return 2
	}

	log := newLogger(cfg.LogFormat)

	a, err := app.New(cfg, log, version)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		return 2
	}
	defer a.Close()

	// Listen before logging, so a busy port or a bad PSP_ADDR is a failed
	// start with a non-zero exit code, not a clean exit.
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", cfg.Addr)
	if err != nil {
		log.Error("cannot listen", "addr", cfg.Addr, "err", err)
		return 1
	}

	srv := &http.Server{
		Handler:           a.Handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	served := make(chan error, 1)
	go func() {
		served <- srv.Serve(ln)
	}()
	log.Info("psp-sandbox listening", "addr", ln.Addr().String(), "version", version, "manual_clock", cfg.ManualClock)

	select {
	case err := <-served:
		log.Error("server stopped", "err", err)
		return 1
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
		return 1
	}
	return 0
}

func newLogger(format string) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}
