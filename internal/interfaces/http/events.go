package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	appfeed "github.com/zkrebbekx/flexitype/application/feed"
	appwebhook "github.com/zkrebbekx/flexitype/application/webhook"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

// eventDelivery gates the webhook-subscription and events-feed surface;
// both need the outbox.
func (s *server) eventDelivery(w http.ResponseWriter, r *http.Request) bool {
	if !application.FromContext(r.Context()).Features().EventDelivery {
		s.featureDisabled(w, "event delivery (enable the outbox)")
		return false
	}
	return true
}

// --- webhook subscriptions ---------------------------------------------------

// subscriptionResponse is the wire form of a subscription. Secrets never
// leave the service.
type subscriptionResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	EventTypes []string  `json:"event_types"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toSubscriptionResponse(sub appwebhook.Subscription) subscriptionResponse {
	types := sub.EventTypes
	if types == nil {
		types = []string{}
	}
	return subscriptionResponse{
		ID:         sub.ID.String(),
		Name:       sub.Name,
		URL:        sub.URL,
		EventTypes: types,
		Active:     sub.Active,
		CreatedAt:  sub.CreatedAt,
		UpdatedAt:  sub.UpdatedAt,
	}
}

type createSubscriptionRequest struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Secret     string   `json:"secret"`
	EventTypes []string `json:"event_types"`
	Active     *bool    `json:"active"`
}

func (s *server) createSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	var req createSubscriptionRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	sub, err := application.FromContext(r.Context()).Webhooks().Create(r.Context(), appwebhook.CreateInput{
		Name:       req.Name,
		URL:        req.URL,
		Secret:     req.Secret,
		EventTypes: req.EventTypes,
		Active:     req.Active,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSubscriptionResponse(*sub))
}

func (s *server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	subs, err := application.FromContext(r.Context()).Webhooks().List(r.Context())
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	items := make([]subscriptionResponse, 0, len(subs))
	for _, sub := range subs {
		items = append(items, toSubscriptionResponse(sub))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	sub, err := application.FromContext(r.Context()).Webhooks().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, toSubscriptionResponse(*sub))
}

type updateSubscriptionRequest struct {
	URL          *string   `json:"url"`
	EventTypes   *[]string `json:"event_types"`
	Active       *bool     `json:"active"`
	RotateSecret *string   `json:"rotate_secret"`
}

func (s *server) updateSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	var req updateSubscriptionRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	sub, err := application.FromContext(r.Context()).Webhooks().Update(r.Context(), appwebhook.UpdateInput{
		ID:           chi.URLParam(r, "id"),
		URL:          req.URL,
		EventTypes:   req.EventTypes,
		Active:       req.Active,
		RotateSecret: req.RotateSecret,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, toSubscriptionResponse(*sub))
}

func (s *server) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	if err := application.FromContext(r.Context()).Webhooks().Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listSubscriptionDeliveries(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	out, err := application.FromContext(r.Context()).Webhooks().ListDeliveries(r.Context(), appwebhook.ListDeliveriesInput{
		SubscriptionID: chi.URLParam(r, "id"),
		Status:         r.URL.Query().Get("status"),
		Page:           pageArgs(r),
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

func (s *server) redeliverWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	if err := application.FromContext(r.Context()).Webhooks().Redeliver(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": appwebhook.StatusPending})
}

// --- events feed ---------------------------------------------------------------

func (s *server) listEvents(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	in, err := feedListInput(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	out, err := application.FromContext(r.Context()).Feed().List(r.Context(), in)
	if err != nil {
		s.writeFeedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// streamEvents tails the feed over SSE. Each event carries its feed_seq
// as the SSE id, so EventSource reconnection resumes via Last-Event-ID.
func (s *server) streamEvents(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	in, err := feedListInput(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		seq, err := strconv.ParseInt(last, 10, 64)
		if err != nil || seq < 0 {
			writeError(w, s.log, domainerrors.NewValidation("Last-Event-ID must be a feed cursor"))
			return
		}
		in.After = seq
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, s.log, domainerrors.NewValidation("streaming is unsupported by this connection"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	interactors := application.FromContext(r.Context())
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		out, err := interactors.Feed().List(r.Context(), in)
		if err != nil {
			// Mid-stream errors terminate the stream; the client
			// reconnects with Last-Event-ID.
			return
		}
		for _, ev := range out.Items {
			data, err := json.Marshal(ev.Envelope)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n",
				ev.Seq, ev.Envelope.Type.String(), data); err != nil {
				return // client went away
			}
		}
		if len(out.Items) > 0 {
			flusher.Flush()
			in.After = out.NextCursor
			continue // drain without waiting while pages are full
		}

		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return // client went away
			}
			flusher.Flush()
		case <-ticker.C:
		}
	}
}

func (s *server) getCursor(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	consumer := chi.URLParam(r, "consumer")
	position, err := application.FromContext(r.Context()).Feed().Cursor(r.Context(), consumer)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"consumer": consumer, "position": position})
}

type commitCursorRequest struct {
	Position int64 `json:"position"`
	Expected int64 `json:"expected"`
}

func (s *server) commitCursor(w http.ResponseWriter, r *http.Request) {
	if !s.eventDelivery(w, r) {
		return
	}
	var req commitCursorRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	consumer := chi.URLParam(r, "consumer")
	err := application.FromContext(r.Context()).Feed().CommitCursor(r.Context(), consumer, req.Position, req.Expected)
	if errors.Is(err, appfeed.ErrCursorConflict) {
		var body errorBody
		body.Error.Code = "CURSOR_CONFLICT"
		body.Error.Message = "the cursor moved since it was read; re-read and retry"
		writeJSON(w, http.StatusConflict, body)
		return
	}
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"consumer": consumer, "position": req.Position})
}

// writeFeedError maps feed sentinels onto their HTTP semantics.
func (s *server) writeFeedError(w http.ResponseWriter, err error) {
	if errors.Is(err, appfeed.ErrGone) {
		var body errorBody
		body.Error.Code = "CURSOR_EXPIRED"
		body.Error.Message = "events at this cursor were pruned; re-baseline from current state"
		writeJSON(w, http.StatusGone, body)
		return
	}
	writeError(w, s.log, err)
}

func feedListInput(r *http.Request) (appfeed.ListInput, error) {
	in := appfeed.ListInput{}
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return in, domainerrors.NewValidation("after must be an integer cursor")
		}
		in.After = after
	}
	if raw := r.URL.Query().Get("types"); raw != "" {
		in.Types = strings.Split(raw, ",")
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return in, domainerrors.NewValidation("limit must be an integer")
		}
		in.Limit = limit
	}
	return in, nil
}
