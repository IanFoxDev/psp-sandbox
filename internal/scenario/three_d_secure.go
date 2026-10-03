package scenario

import "github.com/ianfoxdev/psp-sandbox/internal/payment"

func init() {
	register(Definition{
		Name:        "three_d_secure",
		Description: "pending -> requires_action until the customer authenticates, then the outcome.",
		Params: []Param{{
			Name:        "outcome",
			Type:        "string",
			Default:     "succeeded",
			Allowed:     []string{"succeeded", "declined"},
			Description: "What happens after a successful authentication.",
		}},
		build: func(v *Values) Scenario {
			return threeDSecure{base: base{name: "three_d_secure"}, declined: v.String("outcome") == "declined"}
		},
	})
}

type threeDSecure struct {
	base
	declined bool
}

func (threeDSecure) OnCreate(c CreateContext) Plan {
	return Plan{Steps: []Step{{After: c.ProcessingDelay, Status: payment.RequiresAction}}}
}

// OnAuthenticate settles right away: the customer has already waited.
func (s threeDSecure) OnAuthenticate(c CreateContext, ok bool) []Step {
	switch {
	case !ok:
		return []Step{{Status: payment.Failed, Reason: "authentication_failed"}}
	case s.declined:
		return []Step{{Status: payment.Failed, Reason: "generic_decline"}}
	}
	c.ProcessingDelay = 0
	return []Step{settle(c)}
}
