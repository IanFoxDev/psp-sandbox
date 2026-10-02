package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// HeaderStripeSignature is the header Stripe signs webhooks with.
const HeaderStripeSignature = "Stripe-Signature"

// StripeSigner signs webhooks the way Stripe does: "t=<unix>,v1=<hex>", where
// hex is HMAC-SHA256 over "<t>.<body>" keyed with the whole secret string.
// The official SDKs verify it with their own constructEvent.
type StripeSigner struct {
	secret string
}

// NewStripe takes the secret as the SDKs do: any non-empty string, usually
// "whsec_" followed by random characters.
func NewStripe(secret string) (*StripeSigner, error) {
	if secret == "" {
		return nil, errors.New("webhook secret must not be empty")
	}
	return &StripeSigner{secret: secret}, nil
}

// Secret returns the secret.
func (s *StripeSigner) Secret() string { return s.secret }

// Signature returns the hex v1 signature of body at unix time t.
func (s *StripeSigner) Signature(t int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(strconv.FormatInt(t, 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Sign sets Stripe-Signature on h. The event id is in the body, not in a
// header, so id is not used.
func (s *StripeSigner) Sign(h http.Header, _ string, at time.Time, body []byte) {
	t := at.Unix()
	h.Set(HeaderStripeSignature, "t="+strconv.FormatInt(t, 10)+",v1="+s.Signature(t, body))
}
