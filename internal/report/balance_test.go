package report_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/report"
)

func TestMajor(t *testing.T) {
	for _, c := range []struct {
		minor    int64
		currency string
		want     string
	}{
		{1050, "EUR", "10.50"},
		{5, "usd", "0.05"},
		{-30000, "EUR", "-300.00"},
		{-7, "EUR", "-0.07"},
		{0, "EUR", "0.00"},
		{1050, "JPY", "1050"},
		{-500, "krw", "-500"},
		{123456, "USDT", "1234.56"},
	} {
		if got := report.Major(c.minor, c.currency); got != c.want {
			t.Errorf("Major(%d, %s) = %s, want %s", c.minor, c.currency, got, c.want)
		}
	}
}

func TestBalanceRowsComeFromWhatMovedMoney(t *testing.T) {
	at := time.Date(2026, 10, 1, 23, 30, 0, 0, time.UTC)
	p := payment.Payment{ID: "pay_A", Currency: "EUR", Amount: 1000, Reference: "order-1"}
	captured := p
	captured.Status, captured.CapturedAmount = payment.Captured, 1000
	falseFailed := captured
	falseFailed.Status = payment.Failed
	refunded := captured
	refunded.Status, refunded.RefundedAmount = payment.PartiallyRefunded, 300
	disputed := refunded
	disputed.Status = payment.Disputed
	lost := disputed
	lost.Status = payment.ChargebackLost

	events := []payment.Event{
		{ID: "evt_1", Type: payment.EventPaymentCaptured, CreatedAt: at, Snapshot: captured},
		{ID: "evt_2", Type: payment.EventPaymentFailed, CreatedAt: at, Snapshot: falseFailed},
		{ID: "evt_3", Type: payment.EventRefundSucceeded, CreatedAt: at.Add(time.Hour), Snapshot: refunded,
			Data: payment.Refund{ID: "ref_B", PaymentID: "pay_A", Amount: 300, Currency: "EUR"}},
		{ID: "evt_4", Type: payment.EventChargebackOpened, CreatedAt: at.Add(2 * time.Hour), Snapshot: disputed},
		{ID: "evt_5", Type: payment.EventChargebackClosed, CreatedAt: at.Add(3 * time.Hour), Snapshot: lost},
	}
	var out strings.Builder
	if err := report.WriteCSV(&out, report.Balance(events, report.Native)); err != nil {
		t.Fatal(err)
	}
	want := `balance_transaction_id,created,available_on,currency,gross,fee,net,reporting_category,source_id,payment_intent_id,charge_id
txn_1,2026-10-01 23:30:00,2026-10-03 00:00:00,eur,10.00,0.00,10.00,charge,pay_A,pay_A,pay_A
txn_3,2026-10-02 00:30:00,2026-10-04 00:00:00,eur,-3.00,0.00,-3.00,refund,ref_B,pay_A,pay_A
txn_4,2026-10-02 01:30:00,2026-10-04 00:00:00,eur,-7.00,0.00,-7.00,dispute,pay_A,pay_A,pay_A
`
	if out.String() != want {
		t.Errorf("report:\n%s\nwant:\n%s", out.String(), want)
	}
}
