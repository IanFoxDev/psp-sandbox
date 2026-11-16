// Package report builds the provider-side exports a finance team downloads:
// for now Stripe's itemized Balance report, which reconciliation tools read.
// Rows come from the events the sandbox recorded, so a callback that was
// lost, sent five times or sent out of order is still one row here.
package report

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

// Reporting categories, as Stripe names them.
const (
	CategoryCharge          = "charge"
	CategoryRefund          = "refund"
	CategoryDispute         = "dispute"
	CategoryDisputeReversal = "dispute_reversal"
)

// Header is the column order of the itemized Balance report
// (balance_change_from_activity.itemized) for the columns the sandbox fills.
var Header = []string{
	"balance_transaction_id", "created", "available_on", "currency", "gross", "fee", "net",
	"reporting_category", "source_id", "payment_intent_id", "charge_id",
}

// Naming gives the ids a report shows for a payment. The native profile shows
// the payment id everywhere; the stripe profile shows ch_, re_ and dp_ ids.
type Naming struct {
	Charge  func(p payment.Payment) string
	Dispute func(p payment.Payment) string
}

// Native names everything after the payment.
var Native = Naming{
	Charge:  func(p payment.Payment) string { return p.ID },
	Dispute: func(p payment.Payment) string { return p.ID },
}

// Row is one balance transaction. Amounts are in minor units.
type Row struct {
	ID              string
	Created         time.Time
	Currency        string
	Gross           int64
	Fee             int64
	Category        string
	SourceID        string
	PaymentIntentID string
	ChargeID        string
	// Reference of the payment, for filtering; not a column.
	Reference string
}

// Balance turns events into balance transactions, in the order they happened:
// a capture is a charge, a settled refund a negative refund, an opened
// chargeback a negative dispute, and a won one gives the money back. Events
// that move no money (authorized, failed, a false status sent by a scenario)
// have no row.
func Balance(events []payment.Event, n Naming) []Row {
	var rows []Row
	for _, ev := range events {
		p := ev.Snapshot
		row := Row{
			ID:              "txn_" + suffix(ev.ID),
			Created:         ev.CreatedAt.UTC(),
			Currency:        p.Currency,
			PaymentIntentID: p.ID,
			ChargeID:        n.Charge(p),
			Reference:       p.Reference,
		}
		switch ev.Type {
		case payment.EventPaymentCaptured:
			if p.Status != payment.Captured {
				continue
			}
			row.Category, row.Gross, row.SourceID = CategoryCharge, p.CapturedAmount, n.Charge(p)
		case payment.EventRefundSucceeded:
			r, ok := ev.Data.(payment.Refund)
			if !ok {
				continue
			}
			row.Category, row.Gross, row.SourceID = CategoryRefund, -r.Amount, r.ID
		case payment.EventChargebackOpened:
			if p.Status != payment.Disputed {
				continue
			}
			row.Category, row.Gross, row.SourceID = CategoryDispute, -(p.CapturedAmount - p.RefundedAmount), n.Dispute(p)
		case payment.EventChargebackClosed:
			if p.Status != payment.ChargebackWon {
				continue
			}
			row.Category, row.Gross, row.SourceID = CategoryDisputeReversal, p.CapturedAmount-p.RefundedAmount, n.Dispute(p)
		default:
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

// WriteCSV writes rows with the header, amounts in major units, times in UTC.
// Funds become available two days after the transaction, at midnight UTC.
func WriteCSV(w io.Writer, rows []Row) error {
	out := csv.NewWriter(w)
	if err := out.Write(Header); err != nil {
		return err
	}
	const layout = "2006-01-02 15:04:05"
	for _, r := range rows {
		day := r.Created.Truncate(24 * time.Hour)
		err := out.Write([]string{
			r.ID,
			r.Created.Format(layout),
			day.Add(48 * time.Hour).Format(layout),
			strings.ToLower(r.Currency),
			Major(r.Gross, r.Currency),
			Major(r.Fee, r.Currency),
			Major(r.Gross-r.Fee, r.Currency),
			r.Category,
			r.SourceID,
			r.PaymentIntentID,
			r.ChargeID,
		})
		if err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

// zeroDecimal lists the currencies Stripe charges in whole units.
var zeroDecimal = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true, "KMF": true, "KRW": true,
	"MGA": true, "PYG": true, "RWF": true, "UGX": true, "VND": true, "VUV": true, "XAF": true,
	"XOF": true, "XPF": true,
}

// Major formats an amount in minor units as a decimal in major units:
// 1050 EUR is 10.50, 1050 JPY is 1050. Other currencies have two decimals.
func Major(minor int64, currency string) string {
	if zeroDecimal[strings.ToUpper(currency)] {
		return strconv.FormatInt(minor, 10)
	}
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return sign + strconv.FormatInt(minor/100, 10) + "." + leftPad(strconv.FormatInt(minor%100, 10))
}

func leftPad(s string) string {
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

func suffix(id string) string {
	_, s, _ := strings.Cut(id, "_")
	return s
}
