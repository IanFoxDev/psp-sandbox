package engine

import (
	"errors"
	"testing"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

func TestForcedStatus(t *testing.T) {
	cases := []struct {
		req    ForceRequest
		status payment.Status
		reason string
	}{
		{ForceRequest{Type: payment.EventPaymentAuthorized}, payment.Authorized, ""},
		{ForceRequest{Type: payment.EventPaymentCaptured}, payment.Captured, ""},
		{ForceRequest{Type: payment.EventPaymentFailed}, payment.Failed, "do_not_honor"},
		{ForceRequest{Type: payment.EventPaymentFailed, Reason: "expired_card"}, payment.Failed, "expired_card"},
		{ForceRequest{Type: payment.EventPaymentCanceled}, payment.Canceled, ""},
		{ForceRequest{Type: payment.EventChargebackOpened}, payment.Disputed, ""},
		{ForceRequest{Type: payment.EventChargebackClosed}, payment.ChargebackLost, ""},
		{ForceRequest{Type: payment.EventChargebackClosed, Outcome: "won"}, payment.ChargebackWon, ""},
	}
	for _, c := range cases {
		s, reason, err := forcedStatus(c.req)
		if err != nil || s != c.status || reason != c.reason {
			t.Errorf("%+v: got %s %q %v", c.req, s, reason, err)
		}
	}
	for _, req := range []ForceRequest{
		{Type: payment.EventRefundSucceeded},
		{Type: "payment.exploded"},
		{Type: payment.EventChargebackClosed, Outcome: "draw"},
	} {
		if _, _, err := forcedStatus(req); !errors.Is(err, ErrUnsupportedEvent) {
			t.Errorf("%+v: err = %v", req, err)
		}
	}
}
