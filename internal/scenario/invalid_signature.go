package scenario

import (
	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

func init() {
	register(Definition{
		Name:        "invalid_signature",
		Description: "Every callback of the payment carries a bad signature. The handler must reject it.",
		Params: []Param{{
			Name:        "mode",
			Type:        "string",
			Default:     "wrong_secret",
			Allowed:     []string{"wrong_secret", "stale_timestamp", "missing"},
			Description: "Signed with another secret, with a timestamp 10 minutes old, or not signed at all.",
		}},
		build: func(v *Values) Scenario {
			return invalidSignature{base: base{name: "invalid_signature"}, mode: v.String("mode")}
		},
	})
}

type invalidSignature struct {
	base
	mode string
}

func (s invalidSignature) Deliver(payment.Event) callback.Plan {
	return callback.Plan{Signature: s.mode}
}
