package scenario

import (
	"errors"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

func init() {
	register(Definition{
		Name:        "out_of_order",
		Description: "Events of the payment are held for window and then delivered newest first.",
		Params: []Param{
			{Name: "window", Type: "duration", Default: "2s", Description: "How long events are collected, from the first held one."},
		},
		build: func(v *Values) Scenario {
			window := v.Duration("window")
			if window <= 0 {
				v.errs = append(v.errs, errors.New("window must be longer than 0s"))
			}
			return outOfOrder{base: base{name: "out_of_order"}, window: window}
		},
	})
}

type outOfOrder struct {
	base
	window time.Duration
}

func (s outOfOrder) Deliver(payment.Event) callback.Plan {
	return callback.Plan{Batch: s.window}
}
