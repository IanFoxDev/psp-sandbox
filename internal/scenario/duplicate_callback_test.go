package scenario_test

import (
	"sync"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

func TestDuplicateCallback(t *testing.T) {
	sb := sandboxtest.New(t)
	id := sb.CreatePayment(nil, scenario.Header, "duplicate_callback; times=3")["id"].(string)

	cbs := sb.Receiver.Wait(3)
	for _, cb := range cbs {
		if cb.ID != cbs[0].ID || cb.Type != "payment.captured" || !cb.Signed {
			t.Fatalf("copies differ: %s vs %s", cb.Body, cbs[0].Body)
		}
	}
	sb.Receiver.Quiet(3, 50*time.Millisecond)
	if dels := sb.App.Dispatcher.Deliveries(id); len(dels) != 3 {
		t.Fatalf("%d deliveries, want 3", len(dels))
	}
}

// The reason this scenario exists: a handler that credits on every callback
// credits twice. A handler that deduplicates on the event id credits once.
func TestDuplicateCallbackParallelCatchesNaiveHandler(t *testing.T) {
	sb := sandboxtest.New(t)

	var mu sync.Mutex
	naive, dedup := 0, 0
	seen := map[string]bool{}
	sb.Receiver.Respond(func(c sandboxtest.Callback) int {
		mu.Lock()
		defer mu.Unlock()
		naive++
		if !seen[c.ID] {
			seen[c.ID] = true
			dedup++
		}
		return 200
	})

	sb.CreatePayment(nil, scenario.Header, "duplicate_callback; times=2; parallel=true")
	sb.Receiver.Wait(2)

	mu.Lock()
	defer mu.Unlock()
	if naive != 2 || dedup != 1 {
		t.Fatalf("naive handler credited %d times, deduplicating handler %d times", naive, dedup)
	}
}

func TestDuplicateCallbackInterval(t *testing.T) {
	sb := sandboxtest.New(t)
	sb.CreatePayment(nil, scenario.Header, "duplicate_callback; interval=100ms")

	cbs := sb.Receiver.Wait(2)
	if gap := cbs[1].Received.Sub(cbs[0].Received); gap < 90*time.Millisecond {
		t.Fatalf("copies %v apart, want at least 100ms", gap)
	}
}
