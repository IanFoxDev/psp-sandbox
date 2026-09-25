package signing

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The same vector is used by clients/php/tests/Webhook/VerifierTest.php, so the
// Go signer and the PHP verifier cannot drift apart.
const (
	vectorSecret    = "whsec_dGVzdC1zZWNyZXQ="
	vectorID        = "evt_1"
	vectorTimestamp = 1790330400
	vectorBody      = `{"id":"evt_1","type":"payment.captured"}`
	vectorSignature = "v1,DpPc9pmsF6HhjneOVQvZ7vDaVsOWIJZaIjvPPCJpZs8="
)

func TestVector(t *testing.T) {
	s, err := New(vectorSecret)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Signature(vectorID, vectorTimestamp, []byte(vectorBody)); got != vectorSignature {
		t.Fatalf("signature = %s, want %s", got, vectorSignature)
	}
}

func TestSign(t *testing.T) {
	s, _ := New(vectorSecret)
	h := http.Header{}
	s.Sign(h, vectorID, time.Unix(vectorTimestamp, 0), []byte(vectorBody))
	if h.Get("Webhook-Id") != vectorID || h.Get("Webhook-Timestamp") != "1790330400" || h.Get("Webhook-Signature") != vectorSignature {
		t.Fatalf("headers = %v", h)
	}
}

func TestSecretWithoutPrefix(t *testing.T) {
	s, err := New("dGVzdC1zZWNyZXQ=")
	if err != nil {
		t.Fatal(err)
	}
	if s.Secret() != vectorSecret {
		t.Fatalf("Secret = %q", s.Secret())
	}
}

func TestInvalidSecret(t *testing.T) {
	for _, secret := range []string{"", "whsec_", "whsec_not base64!"} {
		if _, err := New(secret); err == nil {
			t.Errorf("New(%q) accepted an invalid secret", secret)
		}
	}
}

func TestRandomSecret(t *testing.T) {
	a, b := NewRandomSecret(), NewRandomSecret()
	if a == b || !strings.HasPrefix(a, Prefix) {
		t.Fatalf("got %q and %q", a, b)
	}
	if _, err := New(a); err != nil {
		t.Fatal(err)
	}
}
