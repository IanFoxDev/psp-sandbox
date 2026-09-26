// Package app wires the sandbox from a Config: clock, store, dispatcher,
// scenarios, engine and HTTP routes. main and the tests build it the same way.
package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/api"
	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/clock"
	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/control"
	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/ids"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
	"github.com/ianfoxdev/psp-sandbox/internal/signing"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
	"github.com/ianfoxdev/psp-sandbox/internal/ui"
)

// App is a running sandbox without its listener.
type App struct {
	Handler    http.Handler
	Engine     *engine.Engine
	Store      *store.Store
	Dispatcher *callback.Dispatcher
	Catalog    *scenario.Catalog
	Clock      clock.Clock
	// Secret is the webhook signing secret in "whsec_" form.
	Secret string
}

// New builds the sandbox. version is reported by /version and in the
// User-Agent of callbacks.
func New(cfg config.Config, log *slog.Logger, version string) (*App, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	var clk clock.Clock = clock.Real{}
	if cfg.ManualClock {
		clk = clock.NewManual(time.Now().UTC())
	}

	secret := cfg.WebhookSecret
	if secret == "" {
		secret = signing.NewRandomSecret()
		log.Info("generated webhook secret, set PSP_WEBHOOK_SECRET to fix it", "secret", secret)
	}
	signer, err := signing.New(secret)
	if err != nil {
		return nil, fmt.Errorf("PSP_WEBHOOK_SECRET: %w", err)
	}

	defaultSpec, err := scenario.ParseHeader(cfg.DefaultScenario)
	if err != nil {
		return nil, fmt.Errorf("PSP_DEFAULT_SCENARIO: %w", err)
	}

	catalog := scenario.Builtin()
	var rules *scenario.Rules
	if cfg.ScenariosFile != "" {
		if rules, err = scenario.LoadRules(cfg.ScenariosFile, catalog); err != nil {
			return nil, fmt.Errorf("PSP_SCENARIOS_FILE: %w", err)
		}
		log.Info("loaded scenario rules", "file", cfg.ScenariosFile, "rules", rules.Len())
	}

	gen := ids.New(cfg.Seed)
	st := store.New()
	dispatcher := callback.New(callback.Options{
		Clock:     clk,
		Signer:    signer,
		IDs:       gen,
		Retry:     cfg.RetrySchedule,
		Rand:      ids.Rand(cfg.Seed, "jitter"),
		Log:       log,
		UserAgent: "psp-sandbox/" + version,
	})
	eng, err := engine.New(engine.Config{
		ProcessingDelay: cfg.ProcessingDelay,
		CallbackURL:     cfg.CallbackURL,
		Rules:           rules,
		DefaultScenario: defaultSpec,
	}, engine.Deps{Clock: clk, Store: st, Dispatcher: dispatcher, Catalog: catalog, IDs: gen, Log: log})
	if err != nil {
		dispatcher.Close()
		return nil, fmt.Errorf("PSP_DEFAULT_SCENARIO: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(version + "\n"))
	})
	api.New(eng, st, api.Options{APIKey: cfg.APIKey, Log: log}).Register(mux)
	control.New(eng, dispatcher, catalog, clk).Register(mux)
	ui.New(eng, dispatcher, clk, log).Register(mux)

	return &App{
		Handler:    mux,
		Engine:     eng,
		Store:      st,
		Dispatcher: dispatcher,
		Catalog:    catalog,
		Clock:      clk,
		Secret:     signer.Secret(),
	}, nil
}

// Close stops pending callback deliveries.
func (a *App) Close() {
	a.Dispatcher.Close()
}
