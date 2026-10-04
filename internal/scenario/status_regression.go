package scenario

import (
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

func init() {
	register(Definition{
		Name:        "status_regression",
		Description: "After the payment succeeds, a failed callback arrives for it. The payment stays captured.",
		Params: []Param{{
			Name:        "delay",
			Type:        "duration",
			Default:     "1s",
			Description: "Time from the success to the failed callback.",
		}},
		build: func(v *Values) Scenario {
			return statusRegression{base: base{name: "status_regression"}, delay: v.Duration("delay")}
		},
	})
}

type statusRegression struct {
	base
	delay time.Duration
}

func (s statusRegression) OnCreate(c CreateContext) Plan {
	ok := settle(c)
	return Plan{Steps: []Step{ok, {
		After: ok.After + s.delay, Status: payment.Failed, Reason: "generic_decline", EventOnly: true,
	}}}
}
