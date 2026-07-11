package events

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Signature headers sent with every webhook delivery. The signature is
// hex(HMAC-SHA256(secret, timestamp + "." + body)); signing the timestamp
// lets receivers reject replayed deliveries by rejecting stale timestamps.
const (
	HeaderSignature = "X-Flexitype-Signature"
	HeaderTimestamp = "X-Flexitype-Timestamp"
	HeaderEventType = "X-Flexitype-Event"
	HeaderEventID   = "X-Flexitype-Event-Id"
	HeaderDelivery  = "X-Flexitype-Delivery"
)

// DefaultSignatureTolerance is how far a delivery timestamp may drift
// before VerifyRequest rejects it as a possible replay.
const DefaultSignatureTolerance = 5 * time.Minute

// WebhookConfig configures an outbound webhook handler.
type WebhookConfig struct {
	// URL receives POSTed envelope JSON.
	URL string
	// Secret, when non-empty, signs the body with HMAC-SHA256.
	Secret string
	// Headers are added verbatim to every delivery (e.g. auth headers).
	Headers map[string]string
	// Client defaults to an http.Client with a 10s timeout.
	Client *http.Client
}

type webhookHandler struct {
	name   string
	cfg    WebhookConfig
	client *http.Client
}

// NewWebhookHandler delivers envelopes as signed JSON POSTs. Consumers
// register one per receiving endpoint.
func NewWebhookHandler(name string, cfg WebhookConfig) Handler {
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &webhookHandler{name: name, cfg: cfg, client: client}
}

func (w *webhookHandler) Name() string { return w.name }

func (w *webhookHandler) Handle(ctx context.Context, env Envelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderEventType, env.Type.String())
	req.Header.Set(HeaderEventID, env.ID)
	for k, v := range w.cfg.Headers {
		req.Header.Set(k, v)
	}
	if w.cfg.Secret != "" {
		ts := time.Now().UTC().Format(time.RFC3339)
		req.Header.Set(HeaderTimestamp, ts)
		req.Header.Set(HeaderSignature, Sign(w.cfg.Secret, ts, body))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("deliver webhook: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook %s returned status %d", w.cfg.URL, resp.StatusCode)
	}
	return nil
}

// Sign computes the hex HMAC-SHA256 signature over timestamp + "." + body.
// The timestamp is the RFC 3339 value sent in HeaderTimestamp.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature is the receiver-side counterpart of Sign, using a
// constant-time comparison. Callers should also reject timestamps outside
// their tolerance window (see VerifyRequest).
func VerifySignature(secret, timestamp string, body []byte, signature string) bool {
	return hmac.Equal([]byte(Sign(secret, timestamp, body)), []byte(signature))
}

// VerifyRequest verifies a delivery end to end: signature match against
// any of the given secrets (current + previous during rotation) and a
// timestamp within tolerance of now. This is the one call a receiving
// service needs.
func VerifyRequest(secrets []string, timestamp string, body []byte, signature string, tolerance time.Duration, now time.Time) bool {
	ts, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return false
	}
	drift := now.Sub(ts)
	if drift < -tolerance || drift > tolerance {
		return false
	}
	for _, secret := range secrets {
		if secret != "" && VerifySignature(secret, timestamp, body, signature) {
			return true
		}
	}
	return false
}
