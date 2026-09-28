// Command psp-sandbox runs a fake payment provider for tests.
package main

import (
	"context"
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
	os.Exit(run())
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
	ln, err := net.Listen("tcp", cfg.Addr)
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
