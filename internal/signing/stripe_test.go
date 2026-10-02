package signing

import (
	"net/http"
	"testing"
	"time"
)

// The expected value was computed outside Go:
// hmac.new(b"whsec_test_secret", b"1790000000.{\"id\":\"evt_1\"}", sha256).hexdigest()
// The scheme was also checked end to end against stripe-go's webhook.ConstructEvent.
func TestStripeSignature(t *testing.T) {
	s, err := NewStripe("whsec_test_secret")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"evt_1"}`)
	h := http.Header{}
	s.Sign(h, "ignored", time.Unix(1790000000, 0), body)
	want := "t=1790000000,v1=621e30333c3e6f1df3159281b9d0e6983411cc64573a2fb2802f56791c7a24c1"
	if got := h.Get(HeaderStripeSignature); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if _, err := NewStripe(""); err == nil {
		t.Error("empty secret accepted")
	}
}
