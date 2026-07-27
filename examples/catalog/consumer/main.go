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
	"sync"
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
	// net/http serves each delivery on its own goroutine, so this is
	// guarded — and marked in one atomic step, so two concurrent
	// redeliveries of the same event cannot both see it as new.
	seen := &seenSet{ids: map[string]bool{}}

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
		if !seen.markNew(env.ID) {
			log.Printf("duplicate %s (%s) — acked, skipped", env.ID, env.Type)
			w.WriteHeader(http.StatusOK)
			return
		}

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

// seenSet records which event ids this process already handled, so that an
// at-least-once redelivery is a no-op. Every delivery arrives on its own
// goroutine, so the map needs a lock.
//
// A real consumer keeps this in the same store as the work the event
// drives, and in the same transaction, so that a crash between "marked
// seen" and "work done" cannot drop an event. An in-process map also grows
// without bound; bound it by size or age, or key it on a durable store.
type seenSet struct {
	mu  sync.Mutex
	ids map[string]bool
}

// markNew records id and reports whether it was new. The check and the
// write are one step, so two concurrent redeliveries of one event cannot
// both be treated as new.
func (s *seenSet) markNew(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids[id] {
		return false
	}
	s.ids[id] = true
	return true
}
