package callback

import (
	"encoding/json"
	"slices"
	"time"
)

// Plan says how one event is delivered. The zero value sends it once, right away.
// Scenarios build plans; see internal/scenario.
type Plan struct {
	// Copies is how many times the event is sent. 0 and 1 both mean once.
	Copies int
	// Parallel sends all copies at the same moment instead of one after another.
	Parallel bool
	// Interval is the pause between sequential copies.
	Interval time.Duration
	// Delay holds the event this long after it happened.
	Delay time.Duration
	// Drop records the delivery but never sends it.
	Drop bool
	// Batch holds the event with the other events of the payment that come
	// within Batch of the first held one, then queues them all in reverse order.
	// The first held event's Batch sets the window.
	Batch time.Duration
}

// Status of a delivery.
type Status string

// Delivery statuses.
const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusDropped   Status = "dropped"
)

// Delivery is one copy of one event sent to one URL, with every attempt.
type Delivery struct {
	ID        string `json:"id"`
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	PaymentID string `json:"payment_id"`
	URL       string `json:"url"`
	Copy      int    `json:"copy"`
	// ReplayOf is the id of the delivery this one repeats, set by Replay.
	ReplayOf  string          `json:"replay_of,omitempty"`
	Status    Status          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	Body      json.RawMessage `json:"body"`
	Attempts  []Attempt       `json:"attempts"`
}

// Attempt is one HTTP request of a delivery.
type Attempt struct {
	N              int               `json:"n"`
	At             time.Time         `json:"at"`
	RequestHeaders map[string]string `json:"request_headers"`
	StatusCode     int               `json:"status_code,omitempty"`
	ResponseBody   string            `json:"response_body,omitempty"`
	LatencyMS      int64             `json:"latency_ms"`
	Error          string            `json:"error,omitempty"`
}

func (d *Delivery) clone() Delivery {
	c := *d
	c.Attempts = slices.Clone(d.Attempts)
	return c
}
