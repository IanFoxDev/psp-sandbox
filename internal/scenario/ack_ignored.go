package scenario

import (
	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

func init() {
	register(Definition{
		Name:        "ack_ignored",
		Description: "The sandbox treats the app's 2xx as a failure and retries anyway.",
		Params: []Param{{
			Name:        "times",
			Type:        "int",
			Default:     "2",
			Description: "How many 2xx answers of each callback are ignored (1 to 10), on the retry schedule.",
		}},
		build: func(v *Values) Scenario {
			return ackIgnored{base: base{name: "ack_ignored"}, times: v.Int("times", 1, 10)}
		},
	})
}

type ackIgnored struct {
	base
	times int
}

func (s ackIgnored) Deliver(payment.Event) callback.Plan {
	return callback.Plan{IgnoreAcks: s.times}
}
