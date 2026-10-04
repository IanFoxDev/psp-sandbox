package payment

import "time"

// EventType names what happened. It is the "type" field of a callback.
type EventType string

// Event types sent in callbacks.
const (
	// EventPaymentCreated is sent when a payment is created unconfirmed. The
	// native API creates and confirms at once and never sends it.
	EventPaymentCreated        EventType = "payment.created"
	EventPaymentActionRequired EventType = "payment.action_required"
	EventPaymentAuthorized     EventType = "payment.authorized"
	EventPaymentCaptured       EventType = "payment.captured"
	EventPaymentFailed         EventType = "payment.failed"
	EventPaymentCanceled       EventType = "payment.canceled"
	EventRefundSucceeded       EventType = "refund.succeeded"
	EventRefundFailed          EventType = "refund.failed"
	EventChargebackOpened      EventType = "chargeback.opened"
	EventChargebackClosed      EventType = "chargeback.closed"
	// Checkout session events carry the session as Data.
	EventCheckoutCompleted EventType = "checkout.completed"
	EventCheckoutExpired   EventType = "checkout.expired"
)

// EventTypeFor returns the event emitted when a payment enters status s.
// Refund statuses have no payment event: the refund events carry them.
func EventTypeFor(s Status) (EventType, bool) {
	switch s {
	case RequiresAction:
		return EventPaymentActionRequired, true
	case Authorized:
		return EventPaymentAuthorized, true
	case Captured:
		return EventPaymentCaptured, true
	case Failed:
		return EventPaymentFailed, true
	case Canceled:
		return EventPaymentCanceled, true
	case Disputed:
		return EventChargebackOpened, true
	case ChargebackLost, ChargebackWon:
		return EventChargebackClosed, true
	}
	return "", false
}

// Event is something that happened to a payment or refund. Data is a snapshot
// of the object at that moment, as the API would return it.
type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Data      any       `json:"data"`

	PaymentID string `json:"-"`
	// Snapshot is the payment right after the change, for APIs that render
	// events from more than Data.
	Snapshot Payment `json:"-"`
}
