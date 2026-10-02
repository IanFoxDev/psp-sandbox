// Command compat runs stripe-go against psp-sandbox in the stripe profile:
// the calls a backend makes, and the webhooks it gets, verified by the SDK.
// See compat/README.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

type hook struct{ typ, intent string }

// receiver verifies webhooks with the SDK and keeps the ones that pass.
type receiver struct {
	secret   string
	mu       sync.Mutex
	got      []hook
	rejected []string
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	ev, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), rc.secret)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if err != nil {
		rc.rejected = append(rc.rejected, err.Error())
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var obj struct {
		ID            string `json:"id"`
		Object        string `json:"object"`
		PaymentIntent string `json:"payment_intent"`
	}
	_ = json.Unmarshal(ev.Data.Raw, &obj)
	intent := obj.PaymentIntent
	if obj.Object == "payment_intent" {
		intent = obj.ID
	}
	rc.got = append(rc.got, hook{string(ev.Type), intent})
}

func (rc *receiver) has(typ, intent string) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, h := range rc.got {
		if h.typ == typ && h.intent == intent {
			return true
		}
	}
	return false
}

func client(base string, retries int64, timeout time.Duration) *stripe.Client {
	return stripe.NewClient("sk_test_compat", stripe.WithBackends(stripe.NewBackendsWithConfig(&stripe.BackendConfig{
		URL:               stripe.String(base),
		MaxNetworkRetries: stripe.Int64(retries),
		HTTPClient:        &http.Client{Timeout: timeout},
		LeveledLogger:     &stripe.LeveledLogger{Level: stripe.LevelNull},
	})))
}

func main() {
	base, hookURL, secret := os.Getenv("PSP_URL"), os.Getenv("HOOK_URL"), os.Getenv("WEBHOOK_SECRET")
	rc := &receiver{secret: secret}
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = ":9000"
	}
	go func() { _ = http.ListenAndServe(listen, rc) }()

	// Start from an empty sandbox, so the counts below hold on reruns.
	if resp, err := http.Post(base+"/_sandbox/reset", "application/json", nil); err != nil || resp.StatusCode != http.StatusNoContent {
		fmt.Println("FAIL reset the sandbox:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	sc := client(base, 0, 30*time.Second)
	failed := false
	check := func(name string, f func() error) {
		if err := f(); err != nil {
			failed = true
			fmt.Printf("FAIL %s: %v\n", name, err)
			return
		}
		fmt.Printf("ok   %s\n", name)
	}
	params := func(pm string, metadata ...string) *stripe.PaymentIntentCreateParams {
		p := &stripe.PaymentIntentCreateParams{
			Amount: stripe.Int64(1000), Currency: stripe.String("eur"),
			Confirm: stripe.Bool(true), PaymentMethod: stripe.String(pm),
		}
		p.AddMetadata("sandbox_callback_url", hookURL)
		for i := 0; i+1 < len(metadata); i += 2 {
			p.AddMetadata(metadata[i], metadata[i+1])
		}
		return p
	}

	var paid, declined *stripe.PaymentIntent
	check("card payment succeeds", func() (err error) {
		paid, err = sc.V1PaymentIntents.Create(ctx, params("pm_card_visa"))
		if err == nil && paid.Status != stripe.PaymentIntentStatusSucceeded {
			err = fmt.Errorf("status %s", paid.Status)
		}
		return err
	})
	check("decline is a card error with the intent", func() error {
		_, err := sc.V1PaymentIntents.Create(ctx, params("pm_card_visa_chargeDeclinedInsufficientFunds"))
		var se *stripe.Error
		if !errors.As(err, &se) || se.Type != stripe.ErrorTypeCard || se.Code != stripe.ErrorCodeCardDeclined ||
			se.DeclineCode != stripe.DeclineCodeInsufficientFunds || se.PaymentIntent == nil {
			return fmt.Errorf("got %v", err)
		}
		declined = se.PaymentIntent
		return nil
	})
	check("retry after decline with another card", func() error {
		pi, err := sc.V1PaymentIntents.Confirm(ctx, declined.ID, &stripe.PaymentIntentConfirmParams{
			PaymentMethod: stripe.String("pm_card_visa"),
		})
		if err == nil && pi.Status != stripe.PaymentIntentStatusSucceeded {
			err = fmt.Errorf("status %s", pi.Status)
		}
		return err
	})
	check("manual capture of part of the amount", func() error {
		p := params("pm_card_visa")
		p.CaptureMethod = stripe.String("manual")
		pi, err := sc.V1PaymentIntents.Create(ctx, p)
		if err != nil || pi.Status != stripe.PaymentIntentStatusRequiresCapture {
			return fmt.Errorf("authorize: %v %v", err, pi)
		}
		pi, err = sc.V1PaymentIntents.Capture(ctx, pi.ID, &stripe.PaymentIntentCaptureParams{AmountToCapture: stripe.Int64(600)})
		if err == nil && (pi.Status != stripe.PaymentIntentStatusSucceeded || pi.AmountReceived != 600) {
			err = fmt.Errorf("status %s, received %d", pi.Status, pi.AmountReceived)
		}
		return err
	})
	check("partial refund", func() error {
		r, err := sc.V1Refunds.Create(ctx, &stripe.RefundCreateParams{PaymentIntent: stripe.String(paid.ID), Amount: stripe.Int64(300)})
		if err == nil && r.Status != stripe.RefundStatusSucceeded {
			err = fmt.Errorf("status %s", r.Status)
		}
		return err
	})
	check("server error reaches a client without retries", func() error {
		_, err := sc.V1PaymentIntents.Create(ctx, params("pm_card_visa", "sandbox_scenario", "server_error_then_success", "order", "no-retry"))
		var se *stripe.Error
		if !errors.As(err, &se) || se.HTTPStatusCode != http.StatusServiceUnavailable {
			return fmt.Errorf("got %v", err)
		}
		return nil
	})
	// stripe-go turns an error answer into *stripe.Error, whose canRetry allows
	// only 429 lock_timeout, so Stripe-Should-Retry and 5xx are never looked
	// at. The check pins that; if a new stripe-go retries, docs/stripe.md
	// needs updating.
	check("stripe-go does not retry a 503 even with retries on", func() error {
		_, err := client(base, 2, 30*time.Second).V1PaymentIntents.Create(ctx,
			params("pm_card_visa", "sandbox_scenario", "server_error_then_success", "order", "retry"))
		var se *stripe.Error
		if !errors.As(err, &se) || se.HTTPStatusCode != http.StatusServiceUnavailable {
			return fmt.Errorf("got %v", err)
		}
		return nil
	})
	check("retry after a client timeout gets the stored answer", func() error {
		pi, err := client(base, 2, time.Second).V1PaymentIntents.Create(ctx,
			params("pm_card_visa", "sandbox_scenario", "timeout_then_success; delay=3s", "order", "timeout"))
		if err == nil && pi.Status != stripe.PaymentIntentStatusSucceeded {
			err = fmt.Errorf("status %s", pi.Status)
		}
		return err
	})
	check("list pages through all intents", func() error {
		seen := map[string]bool{}
		for pi, err := range sc.V1PaymentIntents.List(ctx, &stripe.PaymentIntentListParams{ListParams: stripe.ListParams{Limit: stripe.Int64(2)}}).All(ctx) {
			if err != nil {
				return err
			}
			if seen[pi.ID] {
				return fmt.Errorf("%s twice", pi.ID)
			}
			seen[pi.ID] = true
		}
		// paid, declined, manual and the one created after the timeout.
		if len(seen) != 4 {
			return fmt.Errorf("%d intents, want 4", len(seen))
		}
		return nil
	})
	check("webhooks verify with webhook.ConstructEvent", func() error {
		want := []hook{
			{"payment_intent.succeeded", paid.ID}, {"charge.refunded", paid.ID},
			{"payment_intent.payment_failed", declined.ID}, {"payment_intent.succeeded", declined.ID},
		}
		deadline := time.Now().Add(10 * time.Second)
		for _, h := range want {
			for !rc.has(h.typ, h.intent) {
				if time.Now().After(deadline) {
					return fmt.Errorf("no %s for %s", h.typ, h.intent)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		rc.mu.Lock()
		defer rc.mu.Unlock()
		if len(rc.rejected) > 0 {
			return fmt.Errorf("rejected: %v", rc.rejected)
		}
		return nil
	})
	check("events retrieve", func() error {
		list := sc.V1Events.List(ctx, &stripe.EventListParams{Type: stripe.String("charge.refunded")})
		for ev, err := range list.All(ctx) {
			if err != nil {
				return err
			}
			got, err := sc.V1Events.Retrieve(ctx, ev.ID, nil)
			if err == nil && got.Type != "charge.refunded" {
				err = fmt.Errorf("type %s", got.Type)
			}
			return err
		}
		return errors.New("no charge.refunded event")
	})
	if failed {
		os.Exit(1)
	}
}
