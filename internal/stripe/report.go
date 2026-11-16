package stripe

import (
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/report"
)

// ReportNaming gives the balance report the ids the API shows: the charge of
// the attempt that was captured, and the dispute of that charge.
var ReportNaming = report.Naming{
	Charge:  func(p payment.Payment) string { return chargeID(p, p.Attempt) },
	Dispute: func(p payment.Payment) string { return "dp_" + suffix(p.ID) },
}
