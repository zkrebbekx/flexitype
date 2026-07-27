package uow

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestAccessLevels(t *testing.T) {
	Convey("Given a deny-list policy that restricts cost", t, func() {
		a := Access{Attr: map[string]Perm{"cost": PermNone, "margin": PermRead}}

		Convey("Then an attribute the policy does not name stays fully accessible", func() {
			So(a.CanRead("sku"), ShouldBeTrue)
			So(a.CanWrite("sku"), ShouldBeTrue)
		})

		Convey("Then a none attribute is neither readable nor writable", func() {
			So(a.CanRead("cost"), ShouldBeFalse)
			So(a.CanWrite("cost"), ShouldBeFalse)
		})

		Convey("Then a read attribute is readable but not writable", func() {
			So(a.CanRead("margin"), ShouldBeTrue)
			So(a.CanWrite("margin"), ShouldBeFalse)
		})
	})

	Convey("Given an allow-list policy that names only sku", t, func() {
		a := Access{Attr: map[string]Perm{"sku": PermWrite}, Default: PermNone}

		Convey("Then sku is reachable", func() {
			So(a.CanRead("sku"), ShouldBeTrue)
			So(a.CanWrite("sku"), ShouldBeTrue)
		})

		Convey("Then an attribute added later is unreachable until it is granted", func() {
			So(a.CanRead("cost"), ShouldBeFalse)
			So(a.CanWrite("cost"), ShouldBeFalse)
		})
	})

	Convey("Given an admin policy", t, func() {
		a := Access{Admin: true, Attr: map[string]Perm{"cost": PermNone}, Default: PermNone}

		Convey("Then admin overrides both the entry and the default", func() {
			So(a.CanRead("cost"), ShouldBeTrue)
			So(a.CanWrite("cost"), ShouldBeTrue)
		})
	})

	Convey("Given the deny-all policy", t, func() {
		a := DenyAll()

		Convey("Then nothing is readable or writable", func() {
			So(a.Admin, ShouldBeFalse)
			So(a.CanRead("sku"), ShouldBeFalse)
			So(a.CanWrite("sku"), ShouldBeFalse)
		})
	})
}

func TestAccessFromContext(t *testing.T) {
	Convey("Given a context with no access policy", t, func() {
		ctx := context.Background()

		Convey("When the process has not required a policy", func() {
			a, stamped := AccessFromContextOK(ctx)

			Convey("Then it resolves to admin and reports that nothing was stamped", func() {
				So(stamped, ShouldBeFalse)
				So(a.Admin, ShouldBeTrue)
				So(AccessFromContext(ctx).CanRead("cost"), ShouldBeTrue)
			})
		})

		Convey("When the process requires a policy", func() {
			// The public setter is deliberately one-way, so this internal test
			// restores the flag rather than calling it.
			failClosed.Store(true)
			t.Cleanup(func() { failClosed.Store(false) })

			a, stamped := AccessFromContextOK(ctx)

			Convey("Then a missing policy denies every attribute", func() {
				So(stamped, ShouldBeFalse)
				So(a.Admin, ShouldBeFalse)
				So(a.CanRead("cost"), ShouldBeFalse)
				So(a.CanWrite("cost"), ShouldBeFalse)
			})

			Convey("Then AccessPolicyRequired reports the posture", func() {
				So(AccessPolicyRequired(), ShouldBeTrue)
			})

			Convey("Then background work that stamps the system policy still runs", func() {
				sys := WithSystemAccess(ctx)
				got, stamped := AccessFromContextOK(sys)
				So(stamped, ShouldBeTrue)
				So(got.Admin, ShouldBeTrue)
			})

			Convey("Then a request that stamps its own policy is unaffected", func() {
				req := WithAccess(ctx, Access{Attr: map[string]Perm{"cost": PermRead}})
				got, stamped := AccessFromContextOK(req)
				So(stamped, ShouldBeTrue)
				So(got.CanRead("cost"), ShouldBeTrue)
				So(got.CanRead("sku"), ShouldBeTrue)
			})
		})
	})
}
