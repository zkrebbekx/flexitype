package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/internal/safedial"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Worker drains due deliveries: claim (short tx) → POST (no tx) → record
// (short tx). Crash recovery comes from the inflight lease — expired
// leases return to pending via ReleaseExpired. Every flexitype replica
// runs workers; SKIP LOCKED claims keep them from colliding.
type Worker struct {
	deliveries  DeliveryStore
	client      *http.Client
	interval    time.Duration
	lease       time.Duration
	batch       int
	concurrency int
	maxAttempts int
	nudge       chan struct{}
	onError     func(error)
	now         func() time.Time
}

// WorkerOption customises a Worker.
type WorkerOption func(*Worker)

// WithWorkerInterval sets the poll interval (default 1s).
func WithWorkerInterval(d time.Duration) WorkerOption {
	return func(w *Worker) { w.interval = d }
}

// WithWorkerConcurrency sets parallel deliveries per pass (default 4).
func WithWorkerConcurrency(n int) WorkerOption {
	return func(w *Worker) { w.concurrency = n }
}

// WithMaxAttempts sets the dead-letter cap.
//
// The default of 25 gives a retry window of about 5 hours, not the 3 days
// this comment used to claim: the backoff is 1s, 4s, 16s, 64s, 256s and then
// capped at 15 minutes, so 25 attempts is 5m45s + 19x15m. Sizing a window for
// an overnight outage means raising the cap or the ceiling, and the arithmetic
// is stated here so the next person does not have to rediscover it.
func WithMaxAttempts(n int) WorkerOption {
	return func(w *Worker) { w.maxAttempts = n }
}

// WithWorkerErrorObserver receives worker-level failures (claim/record
// errors — delivery failures are recorded per row).
func WithWorkerErrorObserver(fn func(error)) WorkerOption {
	return func(w *Worker) { w.onError = fn }
}

// WithHTTPClient overrides the delivery client (default 10s timeout).
func WithHTTPClient(c *http.Client) WorkerOption {
	return func(w *Worker) { w.client = c }
}

// defaultLease must exceed the worst-case time to drain one batch through the
// concurrency semaphore, or the last delivery of a pass starts after its own
// lease expired. At the defaults — batch 32, concurrency 4, HTTP timeout 10s —
// that worst case is (32/4 - 1) x 10s = 70s, so a 60s lease was already too
// short: another replica reclaimed the delivery while the first worker's POST
// was still queued, and both sent it.
//
// 5 minutes leaves margin for a slower endpoint. Worker options that raise the
// batch or lower the concurrency should raise the lease with them; the ratio
// to keep is lease >= (batch / concurrency) x timeout.
const defaultLease = 5 * time.Minute

// NewWorker builds a delivery worker over the store. The default HTTP
// client refuses non-public targets (SSRF guard); override with
// WithHTTPClient (e.g. safedial.NewClient with AllowPrivate for on-prem).
func NewWorker(deliveries DeliveryStore, opts ...WorkerOption) *Worker {
	w := &Worker{
		deliveries:  deliveries,
		client:      safedial.NewClient(safedial.Options{Timeout: 10 * time.Second}),
		interval:    time.Second,
		lease:       defaultLease,
		batch:       32,
		concurrency: 4,
		maxAttempts: 25,
		nudge:       make(chan struct{}, 1),
		now:         uow.UTCNow,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Nudge wakes the worker immediately — the relay calls it after
// expansion so happy-path latency stays milliseconds.
func (w *Worker) Nudge() {
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}

// Run processes deliveries until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		w.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-w.nudge:
		case <-ticker.C:
		}
	}
}

// pass releases expired leases, then claims and delivers until nothing
// is due.
func (w *Worker) pass(ctx context.Context) {
	if _, err := w.deliveries.ReleaseExpired(ctx, w.now()); err != nil {
		w.report(fmt.Errorf("release expired leases: %w", err))
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}
		claimed, err := w.deliveries.ClaimDue(ctx, w.batch, w.lease, w.now())
		if err != nil {
			w.report(fmt.Errorf("claim deliveries: %w", err))
			return
		}
		if len(claimed) == 0 {
			return
		}

		// Deliver under a context that survives shutdown. Cancelling the run
		// context mid-batch used to abort the POSTs AND the write that records
		// them, so an already-successful 2xx was lost, the row stayed inflight
		// until its lease expired, and another replica re-delivered it — a
		// duplicate on every rolling deploy, attributable to the deploy rather
		// than to any consumer fault. The batch is bounded by the lease, so
		// this cannot outlive the shutdown budget by more than that.
		batchCtx := context.WithoutCancel(ctx)
		outcomes := make([]Outcome, 0, len(claimed))
		results := make([]Outcome, len(claimed))
		skipped := make([]bool, len(claimed))
		var wg sync.WaitGroup
		sem := make(chan struct{}, w.concurrency)
		for idx, d := range claimed {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, d ClaimedDelivery) {
				defer wg.Done()
				defer func() { <-sem }()
				// A batch drains through the semaphore, so the last delivery
				// can start long after the claim. Check the lease before
				// spending an HTTP timeout on a delivery another worker may
				// already own: ReleaseExpired on any replica returns an
				// expired lease to pending, and Record would reject the
				// outcome anyway.
				if !w.now().Before(d.LeaseExpiresAt) {
					skipped[idx] = true
					w.report(fmt.Errorf("delivery %s: lease expired before the attempt started; abandoning to the new owner", d.ID))
					return
				}
				results[idx] = w.deliver(batchCtx, d)
			}(idx, d)
		}
		wg.Wait()
		for idx := range results {
			if !skipped[idx] {
				outcomes = append(outcomes, results[idx])
			}
		}

		lost, err := w.deliveries.Record(batchCtx, w.now(), outcomes...)
		if err != nil {
			w.report(fmt.Errorf("record outcomes: %w", err))
			return
		}
		if lost > 0 {
			// The lease lapsed while the POST was in flight and another worker
			// took over. Its outcome stands; ours is dropped rather than
			// overwriting it.
			w.report(fmt.Errorf("discarded %d delivery outcome(s) whose lease was lost mid-attempt", lost))
		}
		// Stop claiming new work once shutdown began; the batch above was
		// finished and recorded first.
		if ctx.Err() != nil {
			return
		}
		// Keep claiming until nothing is due (the top of the loop returns on an
		// empty claim). ClaimDue takes the earliest pending delivery per
		// subscription, so successive rounds advance each subscription's backlog
		// in feed order — a backlog drains RTT-bound in one pass instead of one
		// delivery per subscription per poll.
	}
}

// deliver POSTs one envelope and classifies the outcome. The claim's lease
// travels back on the Outcome, so Record writes only while this worker still
// owns the row.
func (w *Worker) deliver(ctx context.Context, d ClaimedDelivery) Outcome {
	out := Outcome{DeliveryID: d.ID, LeaseExpiresAt: d.LeaseExpiresAt}

	body, err := json.Marshal(d.Envelope)
	if err != nil {
		// Undeliverable by construction; retrying cannot help.
		out.Err = fmt.Sprintf("marshal envelope: %v", err)
		out.Dead = true
		return out
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(body))
	if err != nil {
		out.Err = fmt.Sprintf("build request: %v", err)
		out.Dead = true
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(events.HeaderEventType, d.EventType)
	req.Header.Set(events.HeaderEventID, d.EnvelopeID)
	req.Header.Set(events.HeaderDelivery, d.ID.String())
	if d.Secret != "" {
		ts := w.now().Format(time.RFC3339)
		req.Header.Set(events.HeaderTimestamp, ts)
		req.Header.Set(events.HeaderSignature, events.Sign(d.Secret, ts, body))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return w.retryOrDead(out, d, fmt.Sprintf("deliver: %v", err))
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	out.ResponseCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		out.Delivered = true
		return out
	}
	return w.retryOrDead(out, d, fmt.Sprintf("endpoint returned status %d", resp.StatusCode))
}

func (w *Worker) retryOrDead(out Outcome, d ClaimedDelivery, errMsg string) Outcome {
	out.Err = errMsg
	if d.Attempts+1 >= w.maxAttempts {
		out.Dead = true
		return out
	}
	out.NextAttemptAt = w.now().Add(Backoff(d.Attempts + 1))
	return out
}

func (w *Worker) report(err error) {
	if w.onError != nil {
		w.onError(err)
	}
}

// Backoff returns the exponential, jittered delay before attempt n+1:
// 1s, 4s, 16s, … capped at 15 minutes, ±20% jitter so replicas don't
// retry in lockstep.
func Backoff(attempts int) time.Duration {
	const (
		base    = time.Second
		ceiling = 15 * time.Minute
	)
	d := base
	for i := 1; i < attempts && d < ceiling; i++ {
		d *= 4
	}
	if d > ceiling {
		d = ceiling
	}
	jitter := 1 + (rand.Float64()*0.4 - 0.2)
	return time.Duration(float64(d) * jitter)
}
