package webhook

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// deliveryLog is a DeliveryStore that actually holds rows, so the read-side
// usecases (ListDeliveries, Redeliver) can be observed. The delivery-mechanics
// fake in webhook_test.go deliberately stubs these out.
type deliveryLog struct {
	fakeDeliveryStore
	rows []Delivery
	// lastFilter/lastPage capture what the interactor pushed down, so the
	// tests can assert the filter is translated rather than dropped.
	lastFilter   DeliveryFilter
	lastPage     db.Page
	redelivered  []ulid.ID
	redeliverErr error
}

func (s *deliveryLog) List(_ context.Context, filter DeliveryFilter, page db.Page) ([]Delivery, int, error) {
	s.lastFilter, s.lastPage = filter, page
	var out []Delivery
	for _, d := range s.rows {
		if d.TenantID != filter.TenantID.String() {
			continue
		}
		if filter.Status != "" && d.Status != filter.Status {
			continue
		}
		if !filter.SubscriptionID.IsZero() && filter.SubscriptionID.String() != d.SubscriptionID.String() {
			continue
		}
		out = append(out, d)
	}
	total := len(out)
	if page.Limit > 0 && len(out) > page.FetchLimit() {
		out = out[:page.FetchLimit()]
	}
	return out, total, nil
}

func (s *deliveryLog) Redeliver(_ context.Context, tenant valueobjects.TenantID, id ulid.ID, _ time.Time) error {
	if s.redeliverErr != nil {
		return s.redeliverErr
	}
	for _, d := range s.rows {
		if d.ID.String() == id.String() && d.TenantID == tenant.String() {
			s.redelivered = append(s.redelivered, id)
			return nil
		}
	}
	return domainerrors.NewNotFound("webhook_delivery", id.String())
}

func delivery(sub ulid.ID, status string, tenant string) Delivery {
	return Delivery{
		ID:             ulid.New(),
		SubscriptionID: sub,
		EnvelopeID:     ulid.New().String(),
		TenantID:       tenant,
		EventType:      "flexitype.attribute_value.updated",
		Status:         status,
	}
}

func TestSubscriptionReadSide(t *testing.T) {
	Convey("Given a tenant with one subscription and a delivery log", t, func() {
		subs := newFakeSubscriptionStore()
		deliveries := &deliveryLog{}
		log := &recordedActivity{}
		unit := uow.New(&fakeTransactor{}, events.NewDispatcher(), log)
		i := NewInteractor(unit, subs, deliveries, URLPolicy{AllowPrivate: true})
		ctx := context.Background()

		sub, err := i.Create(ctx, CreateInput{
			Name: "billing", URL: "https://billing.internal/hooks", Secret: "s3cret",
		})
		So(err, ShouldBeNil)

		Convey("When the subscription is fetched by ID", func() {
			got, err := i.Get(ctx, sub.ID.String())

			Convey("Then it is returned intact", func() {
				So(err, ShouldBeNil)
				So(got.ID, ShouldEqual, sub.ID)
				So(got.Name, ShouldEqual, "billing")
			})
		})

		Convey("When a malformed ID is fetched", func() {
			_, err := i.Get(ctx, "not-a-ulid")

			Convey("Then it is a validation error, not a not-found", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(domainerrors.IsNotFound(err), ShouldBeFalse)
			})
		})

		Convey("When a well-formed but unknown ID is fetched", func() {
			_, err := i.Get(ctx, ulid.New().String())

			Convey("Then it is a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When another tenant fetches the subscription", func() {
			other := uow.WithTenant(ctx, valueobjects.TenantID("other"))
			_, err := i.Get(other, sub.ID.String())

			Convey("Then it is invisible across the tenant boundary", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When subscriptions are listed", func() {
			items, err := i.List(ctx)

			Convey("Then only this tenant's subscriptions come back", func() {
				So(err, ShouldBeNil)
				So(items, ShouldHaveLength, 1)
				So(items[0].ID, ShouldEqual, sub.ID)

				other := uow.WithTenant(ctx, valueobjects.TenantID("other"))
				empty, err := i.List(other)
				So(err, ShouldBeNil)
				So(empty, ShouldBeEmpty)
			})
		})

		Convey("When the subscription is deleted", func() {
			err := i.Delete(ctx, sub.ID.String())

			Convey("Then it is gone and the removal is audited without secrets", func() {
				So(err, ShouldBeNil)
				So(subs.items, ShouldBeEmpty)
				last := log.entries[len(log.entries)-1]
				So(last.Entity, ShouldEqual, EntityName)
				So(string(last.Before), ShouldNotContainSubstring, "s3cret")

				_, err = i.Get(ctx, sub.ID.String())
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a delete targets a malformed ID", func() {
			err := i.Delete(ctx, "nope")

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a delete targets another tenant's subscription", func() {
			other := uow.WithTenant(ctx, valueobjects.TenantID("other"))
			err := i.Delete(other, sub.ID.String())

			Convey("Then it is refused and the row survives", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(subs.items, ShouldHaveLength, 1)
			})
		})

		Convey("When an update targets a malformed ID", func() {
			active := false
			_, err := i.Update(ctx, UpdateInput{ID: "nope", Active: &active})

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When an update would make the URL invalid", func() {
			bad := "not-a-url"
			_, err := i.Update(ctx, UpdateInput{ID: sub.ID.String(), URL: &bad})

			Convey("Then the update is rejected and the stored URL is unchanged", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(subs.items[sub.ID.String()].URL, ShouldEqual, "https://billing.internal/hooks")
			})
		})

		Convey("When an update deactivates the subscription", func() {
			active := false
			updated, err := i.Update(ctx, UpdateInput{ID: sub.ID.String(), Active: &active})

			Convey("Then it stops matching any event", func() {
				So(err, ShouldBeNil)
				So(updated.Active, ShouldBeFalse)
				So(updated.Matches("flexitype.attribute_value.updated"), ShouldBeFalse)
			})
		})
	})
}

func TestListDeliveries(t *testing.T) {
	Convey("Given a delivery log spanning two subscriptions and two tenants", t, func() {
		subs := newFakeSubscriptionStore()
		deliveries := &deliveryLog{}
		unit := uow.New(&fakeTransactor{}, events.NewDispatcher(), &recordedActivity{})
		i := NewInteractor(unit, subs, deliveries, URLPolicy{AllowPrivate: true})
		ctx := context.Background()

		subA, subB := ulid.New(), ulid.New()
		deliveries.rows = []Delivery{
			delivery(subA, StatusDelivered, "default"),
			delivery(subA, StatusDead, "default"),
			delivery(subB, StatusPending, "default"),
			delivery(subB, StatusDead, "other"),
		}

		Convey("When deliveries are listed unfiltered", func() {
			out, err := i.ListDeliveries(ctx, ListDeliveriesInput{})

			Convey("Then only the caller's tenant is visible", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 3)
				So(deliveries.lastFilter.TenantID, ShouldEqual, valueobjects.DefaultTenant)
			})
		})

		Convey("When deliveries are filtered by subscription", func() {
			out, err := i.ListDeliveries(ctx, ListDeliveriesInput{SubscriptionID: subA.String()})

			Convey("Then the filter is pushed to the store", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 2)
				So(deliveries.lastFilter.SubscriptionID.String() == subA.String(), ShouldBeTrue)
			})
		})

		Convey("When deliveries are filtered by a known status", func() {
			out, err := i.ListDeliveries(ctx, ListDeliveriesInput{Status: StatusDead})

			Convey("Then only that status comes back for this tenant", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
				So(out.Items[0].Status, ShouldEqual, StatusDead)
			})
		})

		Convey("When deliveries are filtered by an unknown status", func() {
			_, err := i.ListDeliveries(ctx, ListDeliveriesInput{Status: "exploded"})

			Convey("Then the status is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown delivery status")
			})
		})

		Convey("When the subscription filter is malformed", func() {
			_, err := i.ListDeliveries(ctx, ListDeliveriesInput{SubscriptionID: "nope"})

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a non-positive limit is requested", func() {
			zero := 0
			_, err := i.ListDeliveries(ctx, ListDeliveriesInput{Page: db.PageArgs{Limit: &zero}})

			Convey("Then the page args are rejected rather than silently defaulted", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "limit must be a positive integer")
			})
		})

		Convey("When a malformed cursor is supplied", func() {
			cursor := "!!!not-base64!!!"
			_, err := i.ListDeliveries(ctx, ListDeliveriesInput{Page: db.PageArgs{Cursor: &cursor}})

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "invalid cursor")
			})
		})

		Convey("When a page smaller than the result set is requested with a total", func() {
			limit := 2
			out, err := i.ListDeliveries(ctx, ListDeliveriesInput{
				Page: db.PageArgs{Limit: &limit, WantTotal: true},
			})

			Convey("Then the page is trimmed and carries a next cursor and a total", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 2)
				So(out.PageInfo.HasNextPage, ShouldBeTrue)
				So(out.PageInfo.NextCursor, ShouldNotBeNil)
				So(out.PageInfo.HasPreviousPage, ShouldBeFalse)
				So(out.PageInfo.TotalCount, ShouldNotBeNil)
				So(*out.PageInfo.TotalCount, ShouldEqual, 3)

				values, err := db.DecodeKeyset(*out.PageInfo.NextCursor)
				So(err, ShouldBeNil)
				So(values, ShouldResemble, []string{out.Items[1].ID.String()})
			})
		})

		Convey("When no total is requested", func() {
			out, err := i.ListDeliveries(ctx, ListDeliveriesInput{})

			Convey("Then the count is omitted rather than zero", func() {
				So(err, ShouldBeNil)
				So(out.PageInfo.TotalCount, ShouldBeNil)
			})
		})
	})
}

func TestRedeliver(t *testing.T) {
	Convey("Given a dead delivery belonging to the default tenant", t, func() {
		subs := newFakeSubscriptionStore()
		deliveries := &deliveryLog{}
		unit := uow.New(&fakeTransactor{}, events.NewDispatcher(), &recordedActivity{})
		i := NewInteractor(unit, subs, deliveries, URLPolicy{AllowPrivate: true})
		ctx := context.Background()

		dead := delivery(ulid.New(), StatusDead, "default")
		deliveries.rows = []Delivery{dead}

		Convey("When it is requeued", func() {
			err := i.Redeliver(ctx, dead.ID.String())

			Convey("Then the store is asked to return it to pending", func() {
				So(err, ShouldBeNil)
				So(deliveries.redelivered, ShouldHaveLength, 1)
				So(deliveries.redelivered[0].String() == dead.ID.String(), ShouldBeTrue)
			})
		})

		Convey("When another tenant requeues it", func() {
			other := uow.WithTenant(ctx, valueobjects.TenantID("other"))
			err := i.Redeliver(other, dead.ID.String())

			Convey("Then it is refused across the tenant boundary", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(deliveries.redelivered, ShouldBeEmpty)
			})
		})

		Convey("When the ID is malformed", func() {
			err := i.Redeliver(ctx, "nope")

			Convey("Then it is rejected as validation before hitting the store", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(deliveries.redelivered, ShouldBeEmpty)
			})
		})

		Convey("When the store fails", func() {
			deliveries.redeliverErr = errors.New("boom")
			err := i.Redeliver(ctx, dead.ID.String())

			Convey("Then the failure is surfaced", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "boom")
			})
		})
	})
}
