package scenario

import "time"

func init() {
	register(Definition{
		Name: "timeout_then_success",
		Description: "The create call times out on the client side, but the payment is created and captured, " +
			"callback included. A retry with the same Idempotency-Key returns the payment.",
		Params: []Param{
			{Name: "delay", Type: "duration", Default: "35s", Description: "How long the response is held in hold mode."},
			{Name: "mode", Type: "string", Default: "hold", Allowed: []string{"hold", "reset"},
				Description: "hold: answer after delay. reset: close the connection without an answer."},
		},
		build: func(v *Values) Scenario {
			return timeoutThenSuccess{
				base:  base{name: "timeout_then_success"},
				delay: v.Duration("delay"),
				reset: v.String("mode") == "reset",
			}
		},
	})
}

type timeoutThenSuccess struct {
	base
	delay time.Duration
	reset bool
}

func (s timeoutThenSuccess) OnCreate(c CreateContext) Plan {
	plan := s.base.OnCreate(c)
	if s.reset {
		plan.Response = Response{Reset: true}
	} else {
		plan.Response = Response{Delay: s.delay}
	}
	return plan
}
