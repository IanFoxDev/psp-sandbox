// Package signing produces Standard Webhooks signatures
// (webhook-id, webhook-timestamp, webhook-signature) for callback requests.
// See https://www.standardwebhooks.com/.
package signing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Prefix marks a Standard Webhooks secret.
const Prefix = "whsec_"

// Header names set by Sign.
const (
	HeaderID        = "webhook-id"
	HeaderTimestamp = "webhook-timestamp"
	HeaderSignature = "webhook-signature"
)

// Signer signs callback bodies with one secret.
type Signer struct {
	secret string
	key    []byte
}

// New parses a secret of the form "whsec_" + base64. The prefix is optional.
func New(secret string) (*Signer, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, Prefix))
	if err != nil || len(key) == 0 {
		return nil, errors.New("webhook secret must be base64, optionally prefixed with " + Prefix)
	}
	if !strings.HasPrefix(secret, Prefix) {
		secret = Prefix + secret
	}
	return &Signer{secret: secret, key: key}, nil
}

// NewRandomSecret returns a new secret with 24 random bytes.
func NewRandomSecret() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return Prefix + base64.StdEncoding.EncodeToString(b)
}

// Secret returns the secret in "whsec_" form.
func (s *Signer) Secret() string { return s.secret }

// Signature returns "v1," + base64(HMAC-SHA256(key, id.timestamp.body)).
func (s *Signer) Signature(id string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(id))
	mac.Write([]byte{'.'})
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Sign sets the three Standard Webhooks headers on h.
func (s *Signer) Sign(h http.Header, id string, at time.Time, body []byte) {
	ts := at.Unix()
	h.Set(HeaderID, id)
	h.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	h.Set(HeaderSignature, s.Signature(id, ts, body))
}

// Wrong returns a signer with another key, for callbacks that must fail
// verification.
func (s *Signer) Wrong() *Signer {
	key := append([]byte("wrong:"), s.key...)
	return &Signer{secret: Prefix + base64.StdEncoding.EncodeToString(key), key: key}
}
