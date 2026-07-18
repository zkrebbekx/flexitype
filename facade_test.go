package flexitype_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// captureHandler is a consumer-supplied events.Handler, the seam WithHandler
// exists to serve.
type captureHandler struct {
	mu   sync.Mutex
	seen []events.Envelope
}

func (c *captureHandler) Name() string { return "capture" }

func (c *captureHandler) Handle(_ context.Context, env events.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, env)
	return nil
}

func (c *captureHandler) types() []events.Type {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]events.Type, 0, len(c.seen))
	for _, e := range c.seen {
		out = append(out, e.Type)
	}
	return out
}

// capturePublisher is a consumer-supplied events.Publisher — the broker seam.
type capturePublisher struct {
	mu     sync.Mutex
	topics []string
	bodies [][]byte
}

func (p *capturePublisher) Publish(_ context.Context, topic string, message []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topics = append(p.topics, topic)
	p.bodies = append(p.bodies, message)
	return nil
}

func (p *capturePublisher) snapshot() ([]string, [][]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.topics...), append([][]byte(nil), p.bodies...)
}

// seedType creates a type with one string attribute and writes a value,
// producing the domain events consumer hooks observe.
func seedType(ctx context.Context, t *testing.T, svc *flexitype.Service) {
	t.Helper()
	it := svc.Interactors(ctx)
	product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "product", DisplayName: "Product",
	})
	So(err, ShouldBeNil)
	name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: product.ID.String(), InternalName: "name",
		DisplayName: "Name", DataType: "string",
	})
	So(err, ShouldBeNil)
	_, err = it.Values().Set(ctx, appvalue.SetInput{
		AttributeDefinitionID: name.ID.String(), EntityID: "e1",
		TypeDefinitionID: product.ID.String(), Value: json.RawMessage(`"widget"`),
	})
	So(err, ShouldBeNil)
}

func TestFacadeConsumerHooks(t *testing.T) {
	Convey("Given an embedded service with consumer-supplied event hooks", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		Convey("When a plain events.Handler is registered with WithHandler", func() {
			h := &captureHandler{}
			svc := flexitype.NewInMemory(flexitype.WithHandler(h))
			seedType(ctx, t, svc)

			Convey("Then it receives the domain events for the write", func() {
				seen := h.types()
				So(len(seen), ShouldBeGreaterThan, 0)
				So(seen, ShouldContain, events.Type("flexitype.type_definition.created"))
				So(seen, ShouldContain, events.Type("flexitype.attribute_definition.created"))
			})
		})

		Convey("When a handler is registered for a single event type", func() {
			h := &captureHandler{}
			svc := flexitype.NewInMemory(flexitype.WithHandler(
				h, events.WithEventTypes("flexitype.type_definition.created"),
			))
			seedType(ctx, t, svc)

			Convey("Then only that event type reaches it", func() {
				seen := h.types()
				So(seen, ShouldHaveLength, 1)
				So(seen[0], ShouldEqual, events.Type("flexitype.type_definition.created"))
			})
		})

		Convey("When a publisher is registered with WithPublisher and no topic function", func() {
			pub := &capturePublisher{}
			svc := flexitype.NewInMemory(flexitype.WithPublisher("broker", pub, nil))
			seedType(ctx, t, svc)

			Convey("Then envelope JSON is published under the default topic", func() {
				topics, bodies := pub.snapshot()
				So(len(topics), ShouldBeGreaterThan, 0)
				So(topics, ShouldContain, "flexitype.type_definition.created")

				var env events.Envelope
				So(json.Unmarshal(bodies[0], &env), ShouldBeNil)
				So(env.Type, ShouldNotBeEmpty)
				So(env.SchemaVersion, ShouldEqual, events.SchemaVersion)
			})
		})

		Convey("When a publisher is registered with a custom topic function", func() {
			pub := &capturePublisher{}
			svc := flexitype.NewInMemory(flexitype.WithPublisher("broker", pub,
				func(events.Envelope) string { return "all-events" }))
			seedType(ctx, t, svc)

			Convey("Then every envelope is routed to that topic", func() {
				topics, _ := pub.snapshot()
				So(len(topics), ShouldBeGreaterThan, 0)
				for _, tp := range topics {
					So(tp, ShouldEqual, "all-events")
				}
			})
		})

		Convey("When a webhook endpoint is registered with WithWebhook", func() {
			var mu sync.Mutex
			var bodies [][]byte
			var signatures []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, b)
				signatures = append(signatures, r.Header.Get(events.HeaderSignature))
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			svc := flexitype.NewInMemory(flexitype.WithWebhook("hook", events.WebhookConfig{
				URL: srv.URL, Secret: "s3cret",
			}, events.WithEventTypes("flexitype.type_definition.created")))
			seedType(ctx, t, svc)

			Convey("Then the endpoint receives a signed envelope POST", func() {
				mu.Lock()
				defer mu.Unlock()
				So(bodies, ShouldHaveLength, 1)
				So(signatures[0], ShouldNotBeEmpty)

				var env events.Envelope
				So(json.Unmarshal(bodies[0], &env), ShouldBeNil)
				So(env.Type, ShouldEqual, events.Type("flexitype.type_definition.created"))
			})
		})

		Convey("When hooks are registered after construction via Dispatcher", func() {
			svc := flexitype.NewInMemory()
			h := &captureHandler{}
			svc.Dispatcher().Register(h)
			seedType(ctx, t, svc)

			Convey("Then the late-registered handler still receives events", func() {
				So(len(h.types()), ShouldBeGreaterThan, 0)
			})
		})
	})
}

func TestFacadeAccessors(t *testing.T) {
	Convey("Given an in-memory embedded service", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()

		Convey("When its collaborators are requested", func() {
			Convey("Then the factory, dispatcher and GraphQL engine are all live", func() {
				So(svc.Factory(), ShouldNotBeNil)
				So(svc.Dispatcher(), ShouldNotBeNil)
				So(svc.GraphQLEngine(), ShouldNotBeNil)
				So(svc.Interactors(ctx), ShouldNotBeNil)
			})
		})

		Convey("When database-only facilities are requested", func() {
			Convey("Then Migrate is a no-op and the provisioning surfaces are absent", func() {
				So(svc.Migrate(ctx), ShouldBeNil)
				So(svc.NewAccountLookup(time.Minute), ShouldBeNil)
				So(svc.AdminInteractor(), ShouldBeNil)
			})

			Convey("Then BootstrapAdmin refuses without a database", func() {
				token, err := svc.BootstrapAdmin(ctx, "acme", "root")
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(token, ShouldBeEmpty)
			})
		})

		Convey("When the outbox-only surfaces are used without WithOutbox", func() {
			Convey("Then webhook subscriptions are refused with a validation error", func() {
				err := svc.EnsureWebhookSubscription(ctx, "n", "https://example.com/h", "sec")
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "WithOutbox")
			})

			Convey("Then running the outbox relay returns immediately", func() {
				done := make(chan struct{})
				go func() { defer close(done); svc.RunOutboxRelay(context.Background()) }()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("RunOutboxRelay should be a no-op without WithOutbox")
				}
			})
		})

		Convey("When the search index is used without WithSearchIndex", func() {
			n, err := svc.ReindexSearch(ctx, valueobjects.DefaultTenant)

			Convey("Then reindexing is refused with a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "WithSearchIndex")
				So(n, ShouldEqual, 0)
			})
		})
	})
}

func TestFacadeSearchAndRecompute(t *testing.T) {
	Convey("Given an in-memory service with the search index enabled", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory(flexitype.WithSearchIndex())
		seedType(ctx, t, svc)

		Convey("When the index is rebuilt", func() {
			n, err := svc.ReindexSearch(ctx, valueobjects.DefaultTenant)

			Convey("Then the seeded entity is reindexed", func() {
				So(err, ShouldBeNil)
				So(n, ShouldBeGreaterThan, 0)
			})
		})

		Convey("When computed attributes are recomputed for the tenant", func() {
			n, err := svc.RecomputeComputed(ctx, valueobjects.DefaultTenant)

			Convey("Then the recovery pass succeeds", func() {
				So(err, ShouldBeNil)
				So(n, ShouldBeGreaterThanOrEqualTo, 0)
			})
		})
	})
}

func TestFacadeOutboxOptionsIgnoredInMemory(t *testing.T) {
	Convey("Given an in-memory service constructed with delivery options", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		// The in-memory service documents that WithOutbox is ignored (direct
		// dispatch is already synchronous and in-process); these options must
		// therefore be accepted without changing behaviour.
		h := &captureHandler{}
		svc := flexitype.NewInMemory(
			flexitype.WithOutbox(),
			flexitype.WithDeliveryWorker(),
			flexitype.WithEventRetention(48*time.Hour),
			flexitype.WithWebhookAllowPrivate(),
			flexitype.WithHandler(h),
		)

		Convey("When a write is performed", func() {
			seedType(ctx, t, svc)

			Convey("Then events still dispatch directly and the relay stays a no-op", func() {
				So(len(h.types()), ShouldBeGreaterThan, 0)

				done := make(chan struct{})
				go func() { defer close(done); svc.RunOutboxRelay(context.Background()) }()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("outbox is documented as ignored for the in-memory service")
				}
			})
		})
	})
}

func TestFacadeChangeSetScheduler(t *testing.T) {
	Convey("Given an in-memory service with a background-error observer", t, func() {
		var mu sync.Mutex
		var bgErrs []error
		svc := flexitype.NewInMemory(flexitype.WithBackgroundErrorObserver(func(err error) {
			mu.Lock()
			defer mu.Unlock()
			bgErrs = append(bgErrs, err)
		}))

		Convey("When the change-set scheduler runs and its context is cancelled", func() {
			ctx, cancel := context.WithCancel(uow.WithTenant(context.Background(), valueobjects.DefaultTenant))
			done := make(chan struct{})
			go func() { defer close(done); svc.RunChangeSetScheduler(ctx, time.Millisecond) }()

			time.Sleep(30 * time.Millisecond) // let several ticks fire
			cancel()

			Convey("Then it stops promptly and reports no background errors", func() {
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("scheduler did not stop on context cancellation")
				}
				mu.Lock()
				defer mu.Unlock()
				So(bgErrs, ShouldBeEmpty)
			})
		})

		Convey("When the scheduler is started with a non-positive interval", func() {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); svc.RunChangeSetScheduler(ctx, 0) }()
			cancel()

			Convey("Then it falls back to the default interval and still honours cancellation", func() {
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("scheduler did not stop on context cancellation")
				}
			})
		})
	})
}

func TestFacadeAPIHandlerDefaults(t *testing.T) {
	Convey("Given an embedded service mounting its REST API", t, func() {
		Convey("When APIConfig supplies neither a logger nor a health service", func() {
			svc := flexitype.NewInMemory()
			srv := httptest.NewServer(svc.APIHandler(flexitype.APIConfig{}))
			defer srv.Close()

			Convey("Then defaults are filled in and the liveness probe serves", func() {
				resp, err := http.Get(srv.URL + "/healthz")
				So(err, ShouldBeNil)
				defer func() { _ = resp.Body.Close() }()
				So(resp.StatusCode, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When provisioning is requested on an in-memory service", func() {
			svc := flexitype.NewInMemory(flexitype.WithSearchIndex())
			handler := svc.APIHandler(flexitype.APIConfig{EnableProvisioning: true})
			srv := httptest.NewServer(handler)
			defer srv.Close()

			Convey("Then the handler is still built and serves the API", func() {
				So(handler, ShouldNotBeNil)

				resp, err := http.Get(srv.URL + "/readyz")
				So(err, ShouldBeNil)
				defer func() { _ = resp.Body.Close() }()
				So(resp.StatusCode, ShouldBeLessThan, 500)
			})
		})
	})
}
