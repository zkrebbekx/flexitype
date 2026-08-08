//go:build stress

package flexitype_test

import (
	"context"
	"testing"

	"github.com/zkrebbekx/flexitype"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestSaaSPlans re-runs the slow search classes ONCE each over the dataset a
// prior TestStressSaaS left behind, so their SQL can be captured from the
// Postgres statement log and EXPLAIN-ANALYZEd. It seeds nothing and truncates
// nothing.
func TestSaaSPlans(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	it := svc.Interactors(ctx)

	limit := 20
	for _, q := range []struct {
		label string
		query string
		total bool
	}{
		{"float", `score > 999.0`, false},
		{"decimal", `price = "12.12"`, false},
		{"integer", `count = 5`, false},
		{"quantity", `weight > 4.9 kg`, false},
		{"bool-count", `bool_flag = true`, true},
		{"json-count", `has(meta)`, true},
	} {
		if _, err := it.Query().Execute(ctx, appquery.ExecuteInput{
			Type: "account", Query: q.query,
			Page: db.PageArgs{Limit: &limit, WantTotal: q.total},
		}); err != nil {
			t.Fatalf("%s: %v", q.label, err)
		}
		t.Logf("ran %s: %s", q.label, q.query)
	}
}
