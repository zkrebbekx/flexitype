package application

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestFeatureWiringErrors(t *testing.T) {
	Convey("Given event delivery is disabled", t, func() {
		cfg := FactoryConfig{Features: Features{EventDelivery: false}}
		Convey("Then there are no wiring errors even with every store nil", func() {
			So(featureWiringErrors(cfg), ShouldBeEmpty)
		})
	})

	Convey("Given event delivery is enabled but no stores are wired", t, func() {
		cfg := FactoryConfig{Features: Features{EventDelivery: true}}
		probs := featureWiringErrors(cfg)
		Convey("Then one problem names every missing store, so the mistake fails at boot (via NewFactory) not at request time", func() {
			So(probs, ShouldHaveLength, 1)
			So(probs[0], ShouldContainSubstring, "Features.EventDelivery")
			// Sorted, so the message is stable.
			So(probs[0], ShouldContainSubstring, "CursorStore, Deliveries, FeedStore, Outbox, Subscriptions")
		})
	})
}
