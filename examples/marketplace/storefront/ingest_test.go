package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/events"
)

// recorder collects the projection requests a delivery produced.
type recorder struct {
	mu   sync.Mutex
	keys []entityKey
}

func (r *recorder) project(_ context.Context, key entityKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.keys)
}

// signedDelivery builds a webhook request the way flexitype's sender does.
func signedDelivery(tenant, secret string, env events.Envelope, at time.Time) *http.Request {
	body, _ := json.Marshal(env)
	ts := at.Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodPost, "/hook/"+tenant, bytes.NewReader(body))
	req.SetPathValue("tenant", tenant)
	req.Header.Set(events.HeaderTimestamp, ts)
	req.Header.Set(events.HeaderSignature, events.Sign(secret, ts, body))
	return req
}

// valueEnvelope builds a value-set envelope for one entity, as flexitype
// sends it: the entity coordinates are on the ENVELOPE, and the payload
// carries them too.
func valueEnvelope(id, tenant, typeID, entityID string) events.Envelope {
	env := legacyValueEnvelope(id, tenant, typeID, entityID)
	env.TypeDefinitionID = typeID
	env.EntityID = entityID
	return env
}

// legacyValueEnvelope builds the same event as a service older than the
// entity coordinates sent it: the payload names the entity, the envelope does
// not. A delivery in flight across an upgrade looks like this.
func legacyValueEnvelope(id, tenant, typeID, entityID string) events.Envelope {
	payload, _ := json.Marshal(map[string]string{
		"type_definition_id": typeID, "entity_id": entityID,
	})
	return events.Envelope{
		ID:            id,
		Type:          "flexitype.attribute_value.set",
		AggregateType: "attribute_value",
		AggregateID:   "val-" + id,
		TenantID:      tenant,
		Payload:       payload,
	}
}

// TestIngestVerifiesSignatures pins the trust boundary.
//
// The ingest endpoint is reachable by anyone who can reach the storefront. An
// unsigned or wrongly signed body must never reach the projector: it would let
// an unauthenticated caller name any entity in any tenant and drive reads with
// a merchant's credential.
func TestIngestVerifiesSignatures(t *testing.T) {
	Convey("Given a storefront that knows one merchant's webhook secret", t, func() {
		store := newTestStore(t)
		ctx := context.Background()
		So(store.UpsertMerchant(ctx, Merchant{
			Tenant: "merchant-a", DisplayName: "Merchant A",
			Token: "ft_x_y", WebhookSecret: "top-secret",
		}), ShouldBeNil)

		rec := &recorder{}
		// A zero delay projects inline, so each assertion below sees the
		// effect of its own request with no timer to wait on.
		ingest := NewIngest("merchant-a", store, NewDebouncer(0, rec.project, quietLogger()), quietLogger())
		now := time.Now()
		ingest.now = func() time.Time { return now }

		env := valueEnvelope("evt-1", "merchant-a", "type-1", "tee-1")

		Convey("When a correctly signed delivery arrives", func() {
			w := httptest.NewRecorder()
			ingest.ServeHTTP(w, signedDelivery("merchant-a", "top-secret", env, now))

			Convey("Then it is accepted and the entity is projected", func() {
				So(w.Code, ShouldEqual, http.StatusOK)
				So(rec.count(), ShouldEqual, 1)
				So(rec.keys[0].EntityID, ShouldEqual, "tee-1")
				So(rec.keys[0].Tenant, ShouldEqual, "merchant-a")
			})
		})

		Convey("When the signature is computed with the wrong secret", func() {
			w := httptest.NewRecorder()
			ingest.ServeHTTP(w, signedDelivery("merchant-a", "guessed", env, now))

			Convey("Then it is a 401 and nothing is projected", func() {
				So(w.Code, ShouldEqual, http.StatusUnauthorized)
				So(rec.count(), ShouldEqual, 0)
			})
		})

		Convey("When the delivery carries no signature at all", func() {
			body, _ := json.Marshal(env)
			req := httptest.NewRequest(http.MethodPost, "/hook/merchant-a", bytes.NewReader(body))
			req.SetPathValue("tenant", "merchant-a")
			w := httptest.NewRecorder()
			ingest.ServeHTTP(w, req)

			Convey("Then it is a 401 and nothing is projected", func() {
				So(w.Code, ShouldEqual, http.StatusUnauthorized)
				So(rec.count(), ShouldEqual, 0)
			})
		})

		Convey("When the body is altered after signing", func() {
			req := signedDelivery("merchant-a", "top-secret", env, now)
			tampered := valueEnvelope("evt-1", "merchant-a", "type-1", "someone-elses-product")
			body, _ := json.Marshal(tampered)
			req.Body = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)).Body
			w := httptest.NewRecorder()
			ingest.ServeHTTP(w, req)

			Convey("Then it is a 401 and nothing is projected", func() {
				So(w.Code, ShouldEqual, http.StatusUnauthorized)
				So(rec.count(), ShouldEqual, 0)
			})
		})

		Convey("When the timestamp is far outside the tolerance window", func() {
			w := httptest.NewRecorder()
			ingest.ServeHTTP(w, signedDelivery("merchant-a", "top-secret", env, now.Add(-2*time.Hour)))

			Convey("Then the replay is refused", func() {
				So(w.Code, ShouldEqual, http.StatusUnauthorized)
				So(rec.count(), ShouldEqual, 0)
			})
		})

		Convey("When the tenant is not an onboarded merchant", func() {
			w := httptest.NewRecorder()
			ingest.ServeHTTP(w, signedDelivery("merchant-zzz", "top-secret",
				valueEnvelope("evt-2", "merchant-zzz", "type-1", "tee-1"), now))

			Convey("Then it answers exactly like a bad signature, so merchants are not enumerable", func() {
				So(w.Code, ShouldEqual, http.StatusUnauthorized)
				So(rec.count(), ShouldEqual, 0)
			})
		})

		Convey("When a delivery signed for one merchant claims another tenant in its envelope", func() {
			crossed := valueEnvelope("evt-3", "merchant-b", "type-1", "tee-1")
			w := httptest.NewRecorder()
			ingest.ServeHTTP(w, signedDelivery("merchant-a", "top-secret", crossed, now))

			Convey("Then it is refused: the signed envelope decides the tenant", func() {
				So(w.Code, ShouldEqual, http.StatusUnauthorized)
				So(rec.count(), ShouldEqual, 0)
			})
		})

		Convey("When the same event is delivered twice", func() {
			first := httptest.NewRecorder()
			ingest.ServeHTTP(first, signedDelivery("merchant-a", "top-secret", env, now))
			second := httptest.NewRecorder()
			ingest.ServeHTTP(second, signedDelivery("merchant-a", "top-secret", env, now))

			Convey("Then both are acknowledged and the work runs once", func() {
				So(first.Code, ShouldEqual, http.StatusOK)
				So(second.Code, ShouldEqual, http.StatusOK)
				So(rec.count(), ShouldEqual, 1)
			})
		})
	})
}

// TestDebouncerCoalescesABurst covers the write amplification a batch causes.
//
// Writing one product is one value event per field. Projecting each would
// re-read the same entity once per field and rewrite the same row that many
// times.
func TestDebouncerCoalescesABurst(t *testing.T) {
	Convey("Given a debouncer with a short window", t, func() {
		rec := &recorder{}
		d := NewDebouncer(40*time.Millisecond, rec.project, quietLogger())
		key := entityKey{Tenant: "merchant-a", TypeID: "type-1", EntityID: "tee-1"}

		Convey("When eight events for one entity arrive together", func() {
			for range 8 {
				d.Trigger(key)
			}
			d.Wait()

			Convey("Then the entity is projected once", func() {
				So(rec.count(), ShouldEqual, 1)
			})
		})

		Convey("When events for two entities arrive together", func() {
			d.Trigger(key)
			d.Trigger(entityKey{Tenant: "merchant-a", TypeID: "type-1", EntityID: "tee-2"})
			d.Wait()

			Convey("Then each entity is projected once: the window is per entity", func() {
				So(rec.count(), ShouldEqual, 2)
			})
		})

		Convey("When a second burst follows a closed window", func() {
			d.Trigger(key)
			d.Wait()
			d.Trigger(key)
			d.Wait()

			Convey("Then the later change is not swallowed by the earlier window", func() {
				So(rec.count(), ShouldEqual, 2)
			})
		})
	})
}

// TestSeenSetIsBoundedAndConcurrencySafe covers the redelivery guard.
//
// net/http serves each delivery on its own goroutine. An unguarded map is a
// reported race under -race and a process-killing panic in production —
// exactly when the sender is retrying under load.
func TestSeenSetIsBoundedAndConcurrencySafe(t *testing.T) {
	Convey("Given the ingest's seen-event set", t, func() {
		Convey("When one event id is marked from many goroutines at once", func() {
			s := newSeenSet(100)
			var wg sync.WaitGroup
			var mu sync.Mutex
			newCount := 0
			for range 64 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if s.markNew("01JEVENT") {
						mu.Lock()
						newCount++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()

			Convey("Then exactly one caller sees it as new", func() {
				So(newCount, ShouldEqual, 1)
			})
		})

		Convey("When more ids arrive than the set holds", func() {
			s := newSeenSet(3)
			for _, id := range []string{"a", "b", "c", "d"} {
				So(s.markNew(id), ShouldBeTrue)
			}

			Convey("Then the oldest is evicted, so the set cannot grow without bound", func() {
				So(len(s.ids), ShouldEqual, 3)
				So(s.markNew("a"), ShouldBeTrue)  // evicted, so it looks new again
				So(s.markNew("d"), ShouldBeFalse) // still remembered
			})
		})
	})
}

// ingestHarness is a signed-delivery driver over a real ingest endpoint.
type ingestHarness struct {
	tenant string
	ingest *Ingest
	rec    *recorder
	now    time.Time
}

func newIngestHarness(t *testing.T) *ingestHarness {
	t.Helper()
	store := newTestStore(t)
	tenant := "merchant-r"
	if err := store.UpsertMerchant(context.Background(), Merchant{
		Tenant: tenant, DisplayName: "Merchant R",
		Token: "ft_x_y", WebhookSecret: "route-secret",
	}); err != nil {
		t.Fatalf("upsert merchant: %v", err)
	}
	rec := &recorder{}
	h := &ingestHarness{tenant: tenant, rec: rec, now: time.Now()}
	// A zero delay projects inline, so an assertion sees its own delivery.
	h.ingest = NewIngest(tenant, store, NewDebouncer(0, rec.project, quietLogger()), quietLogger())
	h.ingest.now = func() time.Time { return h.now }
	return h
}

func (h *ingestHarness) deliver(env events.Envelope) {
	w := httptest.NewRecorder()
	h.ingest.ServeHTTP(w, signedDelivery(h.tenant, "route-secret", env, h.now))
	So(w.Code, ShouldEqual, http.StatusOK)
}

// triggered lists what the projector was asked to re-read, as "type/entity".
func (h *ingestHarness) triggered() []string {
	out := []string{}
	for _, key := range h.rec.keys {
		out = append(out, key.TypeID+"/"+key.EntityID)
	}
	return out
}

// TestIngestRoutesOnTheEnvelope covers the routing path.
//
// The envelope names the entity an event concerns, so this projector does not
// decode a payload to learn what changed. It still falls back to the payload,
// because a delivery recorded by an older service is in flight across an
// upgrade and must not be dropped.
func TestIngestRoutesOnTheEnvelope(t *testing.T) {
	Convey("Given an ingest endpoint for a known merchant", t, func() {
		harness := newIngestHarness(t)

		Convey("When a value event arrives with coordinates on the envelope", func() {
			env := valueEnvelope("e1", harness.tenant, "type-1", "p-1")
			// Nothing readable in the payload: the router must not need it.
			env.Payload = []byte(`{"unreadable":true}`)
			harness.deliver(env)

			Convey("Then the entity is queued for projection", func() {
				So(harness.triggered(), ShouldResemble, []string{"type-1/p-1"})
			})
		})

		Convey("When an event recorded by an older service arrives", func() {
			harness.deliver(legacyValueEnvelope("e2", harness.tenant, "type-2", "p-2"))

			Convey("Then the payload fallback still routes it", func() {
				So(harness.triggered(), ShouldResemble, []string{"type-2/p-2"})
			})
		})

		Convey("When an event that names no entity arrives", func() {
			env := valueEnvelope("e3", harness.tenant, "", "")
			env.AggregateType = "type_definition"
			env.Type = "flexitype.type_definition.created"
			env.Payload = []byte(`{"internal_name":"product"}`)
			harness.deliver(env)

			Convey("Then nothing is projected: a schema change is not a catalog change", func() {
				So(harness.triggered(), ShouldBeEmpty)
			})
		})
	})
}
