package scenario

import (
	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

func init() {
	register(Definition{
		Name:        "lost_callback",
		Description: "No callbacks for this payment. The status is only visible through GET.",
		build: func(*Values) Scenario {
			return lostCallback{base: base{name: "lost_callback"}}
		},
	})
}

type lostCallback struct {
	base
}

func (lostCallback) Deliver(payment.Event) callback.Plan {
	return callback.Plan{Drop: true}
}
