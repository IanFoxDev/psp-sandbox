package payment

import (
	"maps"
	"slices"
	"time"
)

// SessionStatus of a checkout session.
type SessionStatus string

// Session statuses.
const (
	SessionOpen     SessionStatus = "open"
	SessionComplete SessionStatus = "complete"
	SessionExpired  SessionStatus = "expired"
)

// LineItem is one line of what the customer buys. Amounts are in minor units.
type LineItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UnitAmount int64  `json:"unit_amount"`
	Quantity   int64  `json:"quantity"`
}

// Total is UnitAmount times Quantity.
func (l LineItem) Total() int64 { return l.UnitAmount * l.Quantity }

// Session is a hosted payment page: the customer pays the line items there,
// possibly in several attempts, until it completes or expires. Its payment is
// created on the first attempt.
type Session struct {
	ID                string            `json:"id"`
	Status            SessionStatus     `json:"status"`
	Currency          string            `json:"currency"`
	LineItems         []LineItem        `json:"line_items"`
	SuccessURL        string            `json:"success_url,omitempty"`
	CancelURL         string            `json:"cancel_url,omitempty"`
	ClientReferenceID string            `json:"client_reference_id,omitempty"`
	CustomerEmail     string            `json:"customer_email,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	// PaymentMetadata goes to the payment when it is created.
	PaymentMetadata map[string]string `json:"payment_metadata,omitempty"`
	PaymentID       string            `json:"payment_id,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	ExpiresAt       time.Time         `json:"expires_at"`
	CompletedAt     time.Time         `json:"completed_at,omitzero"`

	// Reference is what the rules file and the reset by prefix match.
	Reference string `json:"-"`
	// CallbackURL is where events of the session and its payment go.
	CallbackURL string `json:"-"`
}

// AmountTotal is the sum of the line items.
func (s Session) AmountTotal() int64 {
	var total int64
	for _, l := range s.LineItems {
		total += l.Total()
	}
	return total
}

// Clone returns a copy that shares no memory with s.
func (s Session) Clone() Session {
	s.LineItems = slices.Clone(s.LineItems)
	s.Metadata = maps.Clone(s.Metadata)
	s.PaymentMetadata = maps.Clone(s.PaymentMetadata)
	return s
}
