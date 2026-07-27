package erasure

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// stubEraser reports a fixed count, or fails.
type stubEraser struct {
	name string
	n    int
	err  error
}

func (s stubEraser) Name() string { return s.name }

func (s stubEraser) RedactEntity(context.Context, db.Tx, valueobjects.TenantID, string) (int, error) {
	return s.n, s.err
}

func (s stubEraser) RedactTenant(context.Context, db.Tx, valueobjects.TenantID) (int, error) {
	return s.n, s.err
}

// TestRedactResiduals covers the fan-out over the configured erasers.
//
// A redaction failure has to fail the erasure. Reporting success while one
// copy of the erased values survives is the exact defect this machinery
// exists to prevent, so a partial redaction must never be reported as a
// completed purge.
func TestRedactResiduals(t *testing.T) {
	ctx := context.Background()
	tenant := valueobjects.DefaultTenant

	Convey("Given several residual erasers", t, func() {
		i := &Interactor{residuals: []ResidualEraser{
			stubEraser{name: "event log", n: 3},
			stubEraser{name: "activity log", n: 4},
		}}

		Convey("When an entity's residuals are redacted", func() {
			n, err := i.redactEntityResiduals(ctx, nil, tenant, "e1")

			Convey("Then the counts are summed", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 7)
			})
		})

		Convey("When a tenant's residuals are redacted", func() {
			n, err := i.redactTenantResiduals(ctx, nil, tenant)

			Convey("Then the counts are summed", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 7)
			})
		})
	})

	Convey("Given an eraser that fails", t, func() {
		boom := errors.New("connection reset")
		i := &Interactor{residuals: []ResidualEraser{
			stubEraser{name: "event log", n: 3},
			stubEraser{name: "activity log", err: boom},
		}}

		Convey("When an entity's residuals are redacted", func() {
			n, err := i.redactEntityResiduals(ctx, nil, tenant, "e1")

			Convey("Then the failure surfaces, naming the store", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "activity log")
				So(errors.Is(err, boom), ShouldBeTrue)
			})

			Convey("Then no partial count is reported as progress", func() {
				So(n, ShouldEqual, 0)
			})
		})

		Convey("When a tenant's residuals are redacted", func() {
			_, err := i.redactTenantResiduals(ctx, nil, tenant)

			Convey("Then it fails rather than reporting a partial erasure", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})

	Convey("Given no erasers configured", t, func() {
		i := &Interactor{}

		Convey("Then redaction is a no-op rather than an error", func() {
			n, err := i.redactEntityResiduals(ctx, nil, tenant, "e1")
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 0)

			n, err = i.redactTenantResiduals(ctx, nil, tenant)
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 0)
		})
	})
}
