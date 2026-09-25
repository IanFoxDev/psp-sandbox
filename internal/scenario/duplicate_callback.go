package scenario

import (
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

func init() {
	register(Definition{
		Name:        "duplicate_callback",
		Description: "Every event is delivered several times with the same event id.",
		Params: []Param{
			{Name: "times", Type: "int", Default: "2", Description: "How many times each event is sent (2 to 20)."},
			{Name: "parallel", Type: "bool", Default: "false", Description: "Send all copies at once to hit race conditions."},
			{Name: "interval", Type: "duration", Default: "0s", Description: "Pause between copies when not parallel."},
		},
		build: func(v *Values) Scenario {
			return duplicateCallback{
				base:     base{name: "duplicate_callback"},
				times:    v.Int("times", 2, 20),
				parallel: v.Bool("parallel"),
				interval: v.Duration("interval"),
			}
		},
	})
}

type duplicateCallback struct {
	base
	times    int
	parallel bool
	interval time.Duration
}

func (s duplicateCallback) Deliver(payment.Event) callback.Plan {
	return callback.Plan{Copies: s.times, Parallel: s.parallel, Interval: s.interval}
}
