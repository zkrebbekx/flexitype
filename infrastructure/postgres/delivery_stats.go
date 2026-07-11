package postgres

import (
	"context"
	"fmt"

	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/metrics"
)

// deliveryStats reports event-delivery depth for the metrics collector via
// cheap indexed COUNT queries, run on each Prometheus scrape.
type deliveryStats struct {
	q db.QueryExecer
}

// NewDeliveryStats builds the delivery-depth stats source.
func NewDeliveryStats(q db.QueryExecer) metrics.DeliveryStats {
	return &deliveryStats{q: q}
}

func (s *deliveryStats) Snapshot(ctx context.Context) (metrics.DeliveryDepth, error) {
	depth := metrics.DeliveryDepth{DeliveriesByStatus: map[string]int64{}}

	if err := s.q.GetContext(ctx, &depth.OutboxPending,
		`SELECT count(*) FROM flexitype_event_outbox WHERE dispatched_at IS NULL`); err != nil {
		return depth, fmt.Errorf("count pending outbox: %w", err)
	}

	var rows []struct {
		Status string `db:"status"`
		N      int64  `db:"n"`
	}
	if err := s.q.SelectContext(ctx, &rows,
		`SELECT status, count(*) AS n FROM flexitype_webhook_delivery GROUP BY status`); err != nil {
		return depth, fmt.Errorf("count deliveries: %w", err)
	}
	for _, r := range rows {
		depth.DeliveriesByStatus[r.Status] = r.N
	}
	return depth, nil
}
