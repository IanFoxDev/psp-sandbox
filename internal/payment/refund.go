package payment

import "time"

// RefundStatus of a refund.
type RefundStatus string

const (
	RefundPending   RefundStatus = "pending"
	RefundSucceeded RefundStatus = "succeeded"
	RefundFailed    RefundStatus = "failed"
)

// Refund returns part or all of a captured payment.
type Refund struct {
	ID            string       `json:"id"`
	PaymentID     string       `json:"payment_id"`
	Status        RefundStatus `json:"status"`
	Amount        int64        `json:"amount"`
	Currency      string       `json:"currency"`
	Reference     string       `json:"reference,omitempty"`
	FailureReason string       `json:"failure_reason,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}
