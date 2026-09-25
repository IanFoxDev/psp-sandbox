package ids

import (
	"regexp"
	"sync"
	"testing"
)

func TestFormat(t *testing.T) {
	id := New("").Next("pay")
	if !regexp.MustCompile(`^pay_[0-9A-HJKMNP-TV-Z]{12}$`).MatchString(id) {
		t.Fatalf("id %q has the wrong format", id)
	}
}

func TestSameSeedSameIDs(t *testing.T) {
	a, b := New("42"), New("42")
	for range 10 {
		if x, y := a.Next("pay"), b.Next("pay"); x != y {
			t.Fatalf("%s != %s", x, y)
		}
	}
	if New("42").Next("pay") == New("43").Next("pay") {
		t.Fatal("different seeds gave the same id")
	}
}

func TestStreamsDiffer(t *testing.T) {
	if Rand("42", "ids").Uint64() == Rand("42", "jitter").Uint64() {
		t.Fatal("two streams of one seed gave the same number")
	}
}

func TestConcurrentUniqueness(t *testing.T) {
	g := New("")
	var mu sync.Mutex
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				id := g.Next("evt")
				mu.Lock()
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 8000 {
		t.Fatalf("got %d unique ids, want 8000", len(seen))
	}
}
