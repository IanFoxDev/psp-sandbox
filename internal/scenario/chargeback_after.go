package scenario

import (
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

func init() {
	register(Definition{
		Name:        "chargeback_after",
		Description: "Captured, then chargeback.opened after delay and chargeback.closed after close_after.",
		Params: []Param{
			{Name: "delay", Type: "duration", Default: "24h", Description: "Time from capture to chargeback.opened."},
			{Name: "outcome", Type: "string", Default: "lost", Allowed: []string{"lost", "won"}, Description: "How the chargeback closes."},
			{Name: "close_after", Type: "duration", Default: "720h", Description: "Time from chargeback.opened to chargeback.closed."},
		},
		build: func(v *Values) Scenario {
			s := chargebackAfter{
				base:       base{name: "chargeback_after"},
				delay:      v.Duration("delay"),
				closeAfter: v.Duration("close_after"),
				closed:     payment.ChargebackLost,
			}
			if v.String("outcome") == "won" {
				s.closed = payment.ChargebackWon
			}
			return s
		},
	})
}

type chargebackAfter struct {
	base
	delay      time.Duration
	closeAfter time.Duration
	closed     payment.Status
}

// OnCreate counts the delays from creation. With manual capture the payment is
// still authorized when the chargeback falls due, so the chargeback is skipped
// unless the test captured it first.
func (s chargebackAfter) OnCreate(c CreateContext) Plan {
	first := settle(c)
	opened := first.After + s.delay
	return Plan{Steps: []Step{
		first,
		{After: opened, Status: payment.Disputed},
		{After: opened + s.closeAfter, Status: s.closed},
	}}
}
