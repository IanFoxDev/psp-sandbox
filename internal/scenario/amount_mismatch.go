package scenario

import (
	"errors"
	"math"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

func init() {
	register(Definition{
		Name:        "amount_mismatch",
		Description: "The payment is captured for another amount than requested.",
		Params: []Param{{
			Name:        "delta",
			Type:        "int",
			Default:     "-1",
			Description: "Captured amount is amount + delta, at least 1 (not 0). Automatic capture only.",
		}},
		build: func(v *Values) Scenario {
			delta := v.Int("delta", math.MinInt32, math.MaxInt32)
			if delta == 0 {
				v.errs = append(v.errs, errors.New("delta must not be 0"))
			}
			return amountMismatch{base: base{name: "amount_mismatch"}, delta: int64(delta)}
		},
	})
}

type amountMismatch struct {
	base
	delta int64
}

func (s amountMismatch) OnCreate(c CreateContext) Plan {
	step := settle(c)
	if step.Status == payment.Captured {
		step.Amount = max(1, c.Payment.Amount+s.delta)
	}
	return Plan{Steps: []Step{step}}
}
