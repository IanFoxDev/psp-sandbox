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
	"github.com/ianfoxdev/psp-sandbox/internal/httpx"
	"github.com/ianfoxdev/psp-sandbox/internal/ids"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
	"github.com/ianfoxdev/psp-sandbox/internal/signing"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
	"github.com/ianfoxdev/psp-sandbox/internal/stripe"
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

	stripeProfile := cfg.Profile == config.ProfileStripe
	secret := cfg.WebhookSecret
	if secret == "" {
		secret = signing.NewRandomSecret()
	}
	var signer interface {
		callback.Signer
		Secret() string
	}
	var encode func(payment.Event) ([]callback.Message, error)
	if stripeProfile {
		s, err := signing.NewStripe(secret)
		if err != nil {
			return nil, fmt.Errorf("PSP_WEBHOOK_SECRET: %w", err)
		}
		signer, encode = s, stripe.Webhooks
	} else {
		s, err := signing.New(secret)
		if err != nil {
			return nil, fmt.Errorf("PSP_WEBHOOK_SECRET: %w", err)
		}
		signer = s
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
		Encode:    encode,
		IDs:       gen,
		Retry:     cfg.RetrySchedule,
		Rand:      ids.Rand(cfg.Seed, "jitter"),
		Log:       log,
		UserAgent: "psp-sandbox/" + version,
	})
	engCfg := engine.Config{
		PublicURL:       cfg.PublicURL,
		ProcessingDelay: cfg.ProcessingDelay,
		CallbackURL:     cfg.CallbackURL,
		Rules:           rules,
		DefaultScenario: defaultSpec,
	}
	if stripeProfile {
		engCfg.PaymentPrefix, engCfg.RefundPrefix = "pi", "re"
	}
	eng, err := engine.New(engCfg, engine.Deps{Clock: clk, Store: st, Dispatcher: dispatcher, Catalog: catalog, IDs: gen, Log: log})
	if err != nil {
		dispatcher.Close()
		return nil, fmt.Errorf("PSP_DEFAULT_SCENARIO: %w", err)
	}

	// Warnings go last, after every check that can refuse to start.
	if cfg.WebhookSecret == "" {
		log.Warn("PSP_WEBHOOK_SECRET is not set, callbacks are signed with a random secret", "secret", secret)
	}
	switch {
	case cfg.CallbackURL != "":
	case stripeProfile:
		log.Warn("PSP_CALLBACK_URL is not set, only payment intents with metadata[sandbox_callback_url] get webhooks")
	default:
		log.Warn("PSP_CALLBACK_URL is not set, only payments created with callback_url get callbacks")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(version + "\n"))
	})
	if stripeProfile {
		stripe.New(eng, st, stripe.Options{
			APIKey: cfg.APIKey, Seed: cfg.Seed, ManualClock: cfg.ManualClock, Log: log,
		}).Register(mux)
	} else {
		api.New(eng, st, api.Options{APIKey: cfg.APIKey, Log: log}).Register(mux)
	}
	control.New(eng, dispatcher, catalog, clk).Register(mux)
	pages := ui.New(eng, dispatcher, clk, log)
	if stripeProfile {
		pages.SetReturnParams(stripe.ReturnParams)
	}
	pages.Register(mux)

	return &App{
		Handler:    httpx.Routes(mux),
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
