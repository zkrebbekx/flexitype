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

// Dispatch envelopes each event and delivers it to every matching handler.
// A failing or panicking handler never blocks the others; all failures are
// joined into the returned error.
func (d *Dispatcher) Dispatch(ctx context.Context, meta Metadata, evts ...Event) error {
	var errs []error
	for _, e := range evts {
		env, err := NewEnvelope(e, meta, d.now())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, reg := range d.registrations {
			if !reg.wants(env.Type) {
				continue
			}
			if err := d.deliver(ctx, reg.handler, env); err != nil {
				errs = append(errs, fmt.Errorf("handler %s: %w", reg.handler.Name(), err))
			}
		}
	}
	return errors.Join(errs...)
}

// DispatchEnvelopes delivers pre-built envelopes (the outbox relay path):
// no re-enveloping, same fan-out and panic isolation as Dispatch.
func (d *Dispatcher) DispatchEnvelopes(ctx context.Context, envs ...Envelope) error {
	var errs []error
	for _, env := range envs {
		for _, reg := range d.registrations {
			if !reg.wants(env.Type) {
				continue
			}
			if err := d.deliver(ctx, reg.handler, env); err != nil {
				errs = append(errs, fmt.Errorf("handler %s: %w", reg.handler.Name(), err))
			}
		}
	}
	return errors.Join(errs...)
}

func (d *Dispatcher) deliver(ctx context.Context, h Handler, env Envelope) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h.Handle(ctx, env)
}
