package main

import (
	"context"
	"fmt"
)

// backfillQuery selects every product a merchant has.
//
// `has(name)` rather than a status filter: the projection stores drafts and
// archived products too, and the shopper read path is what hides them. A
// product that is promoted from draft to active must appear the moment its
// status changes, with no second backfill.
//
// `name` is required on the root product type, so every product matches.
const backfillQuery = `has(name)`

// rootProductType is the type the backfill queries. A query rooted at it also
// returns every subtype's entities, so one query covers apparel, electronics
// and whatever else a merchant declared.
const rootProductType = "product"

// Backfill projects every product a merchant already has.
//
// It exists because a webhook subscription only carries what happens AFTER it
// is registered. A merchant that imported a catalogue before onboarding, or a
// storefront that lost its database, has no events to replay.
//
// It is safe to re-run at any time: each entity goes through the same
// projector as an event, which overwrites the row from the current value set.
// Re-running it is also the repair procedure for a projection that drifted.
func (p *Projector) Backfill(ctx context.Context, tenant string) (int, error) {
	c, err := p.clients.get(ctx, tenant)
	if err != nil {
		return 0, err
	}

	count := 0
	for row, err := range c.Query(ctx, rootProductType, backfillQuery) {
		if err != nil {
			return count, fmt.Errorf("walk products of %s: %w", tenant, err)
		}
		if err := p.Project(ctx, tenant, row.TypeDefinitionID, row.EntityID); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
