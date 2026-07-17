package events

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Handler consumes envelopes. Implementations must be safe for concurrent
// use; the dispatcher may be shared across requests.
type Handler interface {
	// Name identifies the handler in logs and error messages.
	Name() string

	// Handle processes one envelope. Errors are collected, never fatal to
	// other handlers.
	Handle(ctx context.Context, env Envelope) error
}

// BatchHandler is an optional Handler extension. When a handler implements it,
// the dispatcher hands it every envelope of one dispatch that matches its
// filter in a single HandleBatch call, instead of one Handle call per
// envelope. A projection subscriber uses this to coalesce redundant work: an
// import that writes ten values to one entity in a single commit rebuilds that
// entity's projection once, not ten times. HandleBatch must remain safe for
// concurrent use and must leave the same end state as processing each envelope
// individually — the batch is purely a chance to deduplicate.
type BatchHandler interface {
	Handler

	// HandleBatch processes a whole dispatch's matching envelopes at once, in
	// dispatch order. Like Handle, an error is collected, never fatal to other
	// handlers.
	HandleBatch(ctx context.Context, envs []Envelope) error
}

// HandlerFunc adapts a plain function into a Handler — the simplest client
// hook: any func they want to execute per event.
type HandlerFunc struct {
	name string
	fn   func(ctx context.Context, env Envelope) error
}

// NewHandlerFunc wraps fn as a named Handler.
func NewHandlerFunc(name string, fn func(ctx context.Context, env Envelope) error) HandlerFunc {
	return HandlerFunc{name: name, fn: fn}
}

// Name implements Handler.
func (h HandlerFunc) Name() string { return h.name }

// Handle implements Handler.
func (h HandlerFunc) Handle(ctx context.Context, env Envelope) error { return h.fn(ctx, env) }

// registration pairs a handler with its event-type filter.
type registration struct {
	handler Handler
	types   map[Type]struct{} // empty = all types
}

func (r registration) wants(eventType Type) bool {
	if len(r.types) == 0 {
		return true
	}
	_, ok := r.types[eventType]
	return ok
}

// RegisterOption customises a handler registration.
type RegisterOption func(*registration)

// WithEventTypes restricts a handler to the given event types — pass the
// constants domain packages export (e.g. value.EventUpdated). Without this
// option the handler receives every event.
func WithEventTypes(types ...Type) RegisterOption {
	return func(r *registration) {
		if r.types == nil {
			r.types = make(map[Type]struct{}, len(types))
		}
		for _, t := range types {
			r.types[t] = struct{}{}
		}
	}
}

// Dispatcher fans domain events out to registered handlers. Handlers are
// registered at composition time (before serving); Dispatch is safe for
// concurrent use.
type Dispatcher struct {
	registrations []registration
	now           func() time.Time
}

// NewDispatcher creates an empty dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{now: time.Now}
}

// Register adds a handler. Not safe to call concurrently with Dispatch;
// wire handlers up during composition.
func (d *Dispatcher) Register(h Handler, opts ...RegisterOption) {
	reg := registration{handler: h}
	for _, opt := range opts {
		opt(&reg)
	}
	d.registrations = append(d.registrations, reg)
}

// RegisterFunc registers a plain function hook.
func (d *Dispatcher) RegisterFunc(name string, fn func(ctx context.Context, env Envelope) error, opts ...RegisterOption) {
	d.Register(NewHandlerFunc(name, fn), opts...)
}

// Dispatch envelopes each event and delivers the whole set to every matching
// handler. A failing or panicking handler never blocks the others; all
// failures are joined into the returned error.
func (d *Dispatcher) Dispatch(ctx context.Context, meta Metadata, evts ...Event) error {
	envs := make([]Envelope, 0, len(evts))
	var errs []error
	for _, e := range evts {
		env, err := NewEnvelope(e, meta, d.now())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		envs = append(envs, env)
	}
	errs = append(errs, d.deliverAll(ctx, envs)...)
	return errors.Join(errs...)
}

// DispatchEnvelopes delivers pre-built envelopes (the outbox relay path):
// no re-enveloping, same fan-out and panic isolation as Dispatch.
func (d *Dispatcher) DispatchEnvelopes(ctx context.Context, envs ...Envelope) error {
	return errors.Join(d.deliverAll(ctx, envs)...)
}

// deliverAll routes one dispatch's envelopes to every handler. It iterates
// per registration (not per event) so a BatchHandler can see all of its
// matching envelopes at once and coalesce redundant work; a plain Handler
// still receives one Handle call per matching envelope, in dispatch order.
// Handlers are independent subscribers, so delivering handler-by-handler
// rather than event-by-event is equivalent — only per-handler event order
// matters, and that is preserved.
func (d *Dispatcher) deliverAll(ctx context.Context, envs []Envelope) []error {
	var errs []error
	for _, reg := range d.registrations {
		// Narrow to the envelopes this handler subscribes to. An unfiltered
		// handler (empty type set) sees them all without copying.
		matched := envs
		if len(reg.types) != 0 {
			matched = make([]Envelope, 0, len(envs))
			for _, env := range envs {
				if reg.wants(env.Type) {
					matched = append(matched, env)
				}
			}
		}
		if len(matched) == 0 {
			continue
		}
		if bh, ok := reg.handler.(BatchHandler); ok {
			if err := d.deliverBatch(ctx, bh, matched); err != nil {
				errs = append(errs, fmt.Errorf("handler %s: %w", reg.handler.Name(), err))
			}
			continue
		}
		for _, env := range matched {
			if err := d.deliver(ctx, reg.handler, env); err != nil {
				errs = append(errs, fmt.Errorf("handler %s: %w", reg.handler.Name(), err))
			}
		}
	}
	return errs
}

func (d *Dispatcher) deliver(ctx context.Context, h Handler, env Envelope) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h.Handle(ctx, env)
}

func (d *Dispatcher) deliverBatch(ctx context.Context, h BatchHandler, envs []Envelope) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h.HandleBatch(ctx, envs)
}
