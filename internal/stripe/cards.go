package stripe

import (
	"strings"

	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

// testCards maps Stripe's test payment methods to scenarios, so tests written
// for Stripe test mode behave the same here. Names are from
// https://docs.stripe.com/testing. Any other pm_card_* succeeds.
var testCards = map[string]scenario.Spec{
	"pm_card_visa_chargeDeclined":                  declinedWith("generic_decline"),
	"pm_card_visa_chargeDeclinedInsufficientFunds": declinedWith("insufficient_funds"),
	"pm_card_visa_chargeDeclinedLostCard":          declinedWith("lost_card"),
	"pm_card_visa_chargeDeclinedStolenCard":        declinedWith("stolen_card"),
	"pm_card_chargeDeclinedExpiredCard":            declinedWith("expired_card"),
	"pm_card_chargeDeclinedIncorrectCvc":           declinedWith("incorrect_cvc"),
	"pm_card_chargeDeclinedProcessingError":        declinedWith("processing_error"),
	// Stripe opens the dispute within moments of the payment.
	"pm_card_createDispute": {Name: "chargeback_after", Params: map[string]string{"delay": "1s"}},
}

func declinedWith(reason string) scenario.Spec {
	return scenario.Spec{Name: "declined", Params: map[string]string{"reason": reason}}
}

// cardScenario returns the scenario a test payment method stands for, and
// whether the payment method exists at all.
func cardScenario(pm string) (*scenario.Spec, bool) {
	if spec, ok := testCards[pm]; ok {
		return &spec, true
	}
	return nil, strings.HasPrefix(pm, "pm_card_")
}

// decline is how Stripe reports a failed card payment.
type decline struct {
	code        string
	declineCode string
	message     string
}

// declineFor maps a decline reason of the sandbox to Stripe's error fields.
func declineFor(reason string) decline {
	const declined = "Your card was declined."
	switch reason {
	case "insufficient_funds":
		return decline{"card_declined", "insufficient_funds", "Your card has insufficient funds."}
	case "expired_card":
		return decline{"expired_card", "", "Your card has expired."}
	case "incorrect_cvc":
		return decline{"incorrect_cvc", "", "Your card's security code is incorrect."}
	case "processing_error":
		return decline{"processing_error", "", "An error occurred while processing your card. Try again in a little bit."}
	case "fraud_suspected":
		return decline{"card_declined", "fraudulent", declined}
	case "lost_card", "stolen_card", "do_not_honor", "generic_decline":
		return decline{"card_declined", reason, declined}
	}
	return decline{"card_declined", "generic_decline", declined}
}
