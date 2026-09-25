package scenario

import (
	"time"

	"github.com/ghuser/psp-sandbox/internal/callback"
	"github.com/ghuser/psp-sandbox/internal/payment"
)

func init() {
	register(Definition{
		Name:        "delayed_callback",
		Description: "Callbacks are held for delay after the status change.",
		Params: []Param{
			{Name: "delay", Type: "duration", Default: "10s", Description: "How long each callback is held."},
		},
		build: func(v *Values) Scenario {
			return delayedCallback{base: base{name: "delayed_callback"}, delay: v.Duration("delay")}
		},
	})
}

type delayedCallback struct {
	base
	delay time.Duration
}

func (s delayedCallback) Deliver(payment.Event) callback.Plan {
	return callback.Plan{Delay: s.delay}
}
