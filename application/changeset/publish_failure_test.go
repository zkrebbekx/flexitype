package changeset

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestPublishFailureObserver covers the report a scheduled publish makes when
// it fails.
//
// PublishDue discarded each set's error with a bare continue, so a change-set
// that could never publish retried for ever with no log line, no metric and
// no observer callback. The only symptom was a scheduled change that never
// arrived, and the observer #134 introduced for exactly this class of failure
// never saw it.
func TestPublishFailureObserver(t *testing.T) {
	Convey("Given an interactor with no observer wired", t, func() {
		i := &Interactor{}

		Convey("Then reporting a failure is a no-op rather than a panic", func() {
			So(func() {
				i.reportPublishFailure(ChangeSet{ID: ulid.New()}, errors.New("boom"))
			}, ShouldNotPanic)
		})
	})

	Convey("Given an interactor with an observer", t, func() {
		var seen []string
		var seenErr error
		i := &Interactor{}
		i.OnPublishFailure(func(cs ChangeSet, err error) {
			seen = append(seen, cs.Name)
			seenErr = err
		})

		Convey("When a set fails to publish", func() {
			boom := errors.New("attribute was archived")
			i.reportPublishFailure(ChangeSet{
				ID: ulid.New(), Name: "spring pricing", TenantID: valueobjects.DefaultTenant,
			}, boom)

			Convey("Then the observer sees the set and the cause", func() {
				So(seen, ShouldResemble, []string{"spring pricing"})
				So(errors.Is(seenErr, boom), ShouldBeTrue)
			})
		})
	})

	Convey("Given a scheduler tick with nothing due", t, func() {
		i := &Interactor{store: emptyStore{}, now: func() time.Time { return time.Unix(0, 0).UTC() }}

		Convey("Then it publishes nothing and reports no error", func() {
			n, err := i.PublishDue(context.Background())
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 0)
		})
	})
}

// emptyStore has nothing due.
type emptyStore struct{ Store }

func (emptyStore) DueForPublish(context.Context, time.Time) ([]ChangeSet, error) { return nil, nil }
