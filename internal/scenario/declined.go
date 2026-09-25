package scenario

import "github.com/ianfoxdev/psp-sandbox/internal/payment"

func init() {
	register(Definition{
		Name:        "declined",
		Description: "pending -> failed, payment.failed with the decline reason.",
		Params: []Param{{
			Name:        "reason",
			Type:        "string",
			Default:     "insufficient_funds",
			Allowed:     []string{"insufficient_funds", "do_not_honor", "expired_card", "fraud_suspected"},
			Description: "Decline reason, sent as failure_reason.",
		}},
		build: func(v *Values) Scenario {
			return declined{base: base{name: "declined"}, reason: v.String("reason")}
		},
	})
}

type declined struct {
	base
	reason string
}

func (s declined) OnCreate(c CreateContext) Plan {
	return Plan{Steps: []Step{{After: c.ProcessingDelay, Status: payment.Failed, Reason: s.reason}}}
}
