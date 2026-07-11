// Command consumer is a minimal, production-shaped webhook receiver for
// flexitype events. It verifies the HMAC signature (rejecting unsigned or
// stale requests), acknowledges fast with 2xx, and logs each event. It is
// the reference for how another service consumes flexitype's event stream
// over webhooks.
//
// Run it with the same secret the subscription was created with:
//
//	FLEXITYPE_WEBHOOK_SECRET=super-secret CONSUMER_ADDR=:9100 go run ./examples/catalog/consumer
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/zkrebbekx/flexitype/pkg/events"
)

func main() {
	addr := envOr("CONSUMER_ADDR", ":9100")
	secret := os.Getenv("FLEXITYPE_WEBHOOK_SECRET")
	if secret == "" {
		log.Fatal("FLEXITYPE_WEBHOOK_SECRET is required")
	}
	// Accept the current secret plus an optional previous one, so a
	// receiver keeps working across a subscription secret rotation.
	secrets := []string{secret}
	if prev := os.Getenv("FLEXITYPE_WEBHOOK_SECRET_PREVIOUS"); prev != "" {
		secrets = append(secrets, prev)
	}

	// Track seen event IDs so a redelivery (at-least-once) is a no-op.
	seen := map[string]bool{}

	http.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		// Verify signature + timestamp freshness (5 min tolerance). An
		// invalid signature is a 401 — never process unauthenticated data.
		ts := r.Header.Get(events.HeaderTimestamp)
		sig := r.Header.Get(events.HeaderSignature)
		if !events.VerifyRequest(secrets, ts, body, sig, 5*time.Minute, time.Now()) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		var env events.Envelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}

		// Idempotency: dedupe on the envelope id (delivery is at-least-once).
		if seen[env.ID] {
			log.Printf("duplicate %s (%s) — acked, skipped", env.ID, env.Type)
			w.WriteHeader(http.StatusOK)
			return
		}
		seen[env.ID] = true

		// Acknowledge quickly; do real work asynchronously in a real
		// consumer so a slow handler never trips the sender's retry.
		log.Printf("event %s type=%s aggregate=%s/%s tenant=%s",
			env.ID, env.Type, env.AggregateType, env.AggregateID, env.TenantID)
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("catalog webhook consumer listening on %s", addr)
	srv := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
