package webhook

import (
	"context"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// EntityName is the activity-log entity for subscription changes.
const EntityName = "webhook_subscription"

// Interactor implements the subscription-management usecases.
type Interactor struct {
	uow        uow.UnitOfWork
	subs       SubscriptionStore
	deliveries DeliveryStore
	now        func() time.Time
}

// NewInteractor wires the webhook usecases.
func NewInteractor(u uow.UnitOfWork, subs SubscriptionStore, deliveries DeliveryStore) *Interactor {
	return &Interactor{uow: u, subs: subs, deliveries: deliveries, now: time.Now}
}

// CreateInput registers a new subscription.
type CreateInput struct {
	Name       string
	URL        string
	Secret     string
	EventTypes []string
	Active     *bool // nil defaults to true
}

// Create registers a webhook subscription.
func (i *Interactor) Create(ctx context.Context, in CreateInput) (*Subscription, error) {
	tenant := uow.TenantFromContext(ctx)
	now := i.now().UTC()

	sub := Subscription{
		ID:         ulid.New(),
		TenantID:   tenant,
		Name:       in.Name,
		URL:        in.URL,
		Secret:     in.Secret,
		EventTypes: in.EventTypes,
		Active:     in.Active == nil || *in.Active,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := sub.Validate(); err != nil {
		return nil, err
	}

	err := i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		store := i.subs.WithTx(tx)
		existing, err := store.GetByName(ctx, tenant, in.Name)
		if err != nil && !domainerrors.IsNotFound(err) {
			return fmt.Errorf("check subscription name: %w", err)
		}
		if err == nil {
			return domainerrors.NewConflict("a webhook subscription with this name already exists",
				"name", existing.Name)
		}
		if err := store.Create(ctx, sub); err != nil {
			return fmt.Errorf("create subscription: %w", err)
		}
		c.RecordChange(activity.Change{
			Entity:   EntityName,
			EntityID: sub.ID.String(),
			Action:   activity.ActionCreated,
			After:    redact(sub),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// Ensure upserts a subscription by name — the bootstrap path for
// environment-configured endpoints. Existing subscriptions get the given
// URL/secret/filters; missing ones are created.
func (i *Interactor) Ensure(ctx context.Context, in CreateInput) (*Subscription, error) {
	existing, err := i.subs.GetByName(ctx, uow.TenantFromContext(ctx), in.Name)
	if domainerrors.IsNotFound(err) {
		return i.Create(ctx, in)
	}
	if err != nil {
		return nil, err
	}

	update := UpdateInput{
		ID:         existing.ID.String(),
		URL:        &in.URL,
		EventTypes: &in.EventTypes,
		Active:     in.Active,
	}
	if in.Secret != existing.Secret {
		update.RotateSecret = &in.Secret
	}
	return i.Update(ctx, update)
}

// UpdateInput mutates a subscription. Nil pointers leave fields unchanged.
type UpdateInput struct {
	ID         string
	URL        *string
	EventTypes *[]string
	Active     *bool
	// RotateSecret installs a new secret; the previous one stays
	// signing-valid until the next rotation.
	RotateSecret *string
}

// Update mutates a subscription.
func (i *Interactor) Update(ctx context.Context, in UpdateInput) (*Subscription, error) {
	id, err := ulid.Parse(in.ID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)

	var out Subscription
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		store := i.subs.WithTx(tx)
		sub, err := store.Get(ctx, tenant, id)
		if err != nil {
			return err
		}
		before := redact(sub)

		if in.URL != nil {
			sub.URL = *in.URL
		}
		if in.EventTypes != nil {
			sub.EventTypes = *in.EventTypes
		}
		if in.Active != nil {
			sub.Active = *in.Active
		}
		if in.RotateSecret != nil {
			sub.PreviousSecret = sub.Secret
			sub.Secret = *in.RotateSecret
		}
		sub.UpdatedAt = i.now().UTC()
		if err := sub.Validate(); err != nil {
			return err
		}
		if err := store.Update(ctx, sub); err != nil {
			return fmt.Errorf("update subscription: %w", err)
		}
		c.RecordChange(activity.Change{
			Entity:   EntityName,
			EntityID: sub.ID.String(),
			Action:   activity.ActionUpdated,
			Before:   before,
			After:    redact(sub),
		})
		out = sub
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete removes a subscription and its delivery history.
func (i *Interactor) Delete(ctx context.Context, rawID string) error {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)

	return i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		store := i.subs.WithTx(tx)
		sub, err := store.Get(ctx, tenant, id)
		if err != nil {
			return err
		}
		if err := store.Delete(ctx, tenant, id); err != nil {
			return fmt.Errorf("delete subscription: %w", err)
		}
		c.RecordChange(activity.Change{
			Entity:   EntityName,
			EntityID: sub.ID.String(),
			Action:   activity.ActionRemoved,
			Before:   redact(sub),
		})
		return nil
	})
}

// Get returns one subscription.
func (i *Interactor) Get(ctx context.Context, rawID string) (*Subscription, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	sub, err := i.subs.Get(ctx, uow.TenantFromContext(ctx), id)
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// List returns the tenant's subscriptions.
func (i *Interactor) List(ctx context.Context) ([]Subscription, error) {
	return i.subs.List(ctx, uow.TenantFromContext(ctx))
}

// ListDeliveriesInput filters the delivery log.
type ListDeliveriesInput struct {
	SubscriptionID string
	Status         string
	Page           db.PageArgs
}

// ListDeliveriesOutput is one page of deliveries.
type ListDeliveriesOutput struct {
	Items    []Delivery
	PageInfo db.PageInfo
}

// ListDeliveries pages a subscription's delivery log.
func (i *Interactor) ListDeliveries(ctx context.Context, in ListDeliveriesInput) (*ListDeliveriesOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	filter := DeliveryFilter{TenantID: uow.TenantFromContext(ctx), Status: in.Status}
	if in.SubscriptionID != "" {
		id, err := ulid.Parse(in.SubscriptionID)
		if err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
		filter.SubscriptionID = id
	}
	switch in.Status {
	case "", StatusPending, StatusInflight, StatusDelivered, StatusDead:
	default:
		return nil, domainerrors.NewValidation("unknown delivery status", "status", in.Status)
	}

	items, total, err := i.deliveries.List(ctx, filter, page)
	if err != nil {
		return nil, err
	}
	return &ListDeliveriesOutput{
		Items:    items,
		PageInfo: db.BuildPageInfo(page, len(items), total),
	}, nil
}

// Redeliver returns a dead or delivered delivery to the queue.
func (i *Interactor) Redeliver(ctx context.Context, rawID string) error {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	return i.deliveries.Redeliver(ctx, uow.TenantFromContext(ctx), id, i.now().UTC())
}

// redact strips secrets from activity descriptors.
func redact(s Subscription) Subscription {
	s.Secret = ""
	s.PreviousSecret = ""
	return s
}
