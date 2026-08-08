package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/client"
)

// newOnboarder wires a real onboarder against the shared test flexitype and a
// fake storefront.
func newOnboarder(t *testing.T, store *Store, sf *fakeStorefront, ft testFlexitype) *Onboarder {
	t.Helper()
	admin, err := client.New(ft.url, client.WithToken(ft.adminToken))
	if err != nil {
		t.Fatalf("build admin client: %v", err)
	}
	return NewOnboarder(store, admin, ft.url,
		NewStorefrontClient(sf.server.URL, "internal-token"),
		"http://storefront.test/hook", quietLogger())
}

// TestOnboardingIsIdempotent pins the property the whole control plane rests
// on.
//
// Onboarding spans three services — flexitype's tenant and account API, the
// merchant table, the storefront's internal API — with no distributed
// transaction between them. A failure part-way leaves the earlier steps
// applied, and the only safe repair is to run the whole sequence again. So a
// second run must neither duplicate anything nor fail.
func TestOnboardingIsIdempotent(t *testing.T) {
	Convey("Given a platform with no merchants", t, func() {
		ft := newFlexitype(t)
		store := newTestStore(t)
		sf := newFakeStorefront(t, "internal-token")
		onboarder := newOnboarder(t, store, sf, ft)
		ctx := context.Background()
		tenant := newTenant("alpine")

		in := OnboardInput{ID: tenant, DisplayName: "Alpine Apparel", Tenant: tenant}

		Convey("When a merchant is onboarded", func() {
			first, err := onboarder.Onboard(ctx, in)
			So(err, ShouldBeNil)
			So(first.Tenant, ShouldEqual, tenant)
			So(first.Token, ShouldNotBeEmpty)

			merchantClient, err := client.New(ft.url, client.WithToken(first.Token))
			So(err, ShouldBeNil)

			Convey("Then the tenant has the ecommerce starter schema", func() {
				page, err := merchantClient.Types().List(ctx, client.ListTypesOptions{
					InternalNames: []string{"product"},
				})
				So(err, ShouldBeNil)
				So(page.Items, ShouldHaveLength, 1)
			})

			Convey("Then the storefront holds the merchant's credential and was backfilled", func() {
				reg, ok := sf.lastRegistration()
				So(ok, ShouldBeTrue)
				So(reg.Tenant, ShouldEqual, tenant)
				So(reg.Token, ShouldEqual, first.Token)
				So(reg.WebhookSecret, ShouldEqual, first.WebhookSecret)
				So(sf.backfillCount(), ShouldEqual, 1)
			})

			Convey("Then one webhook subscription points at that merchant's hook path", func() {
				subs, err := merchantClient.Webhooks().List(ctx)
				So(err, ShouldBeNil)
				So(subs, ShouldHaveLength, 1)
				So(subs[0].URL, ShouldEqual, "http://storefront.test/hook/"+tenant)
			})

			Convey("When the same merchant is onboarded a second time", func() {
				second, err := onboarder.Onboard(ctx, in)

				Convey("Then it succeeds and nothing is duplicated", func() {
					So(err, ShouldBeNil)
					So(second.Token, ShouldEqual, first.Token)
					So(second.WebhookSecret, ShouldEqual, first.WebhookSecret)

					merchants, err := store.List(ctx)
					So(err, ShouldBeNil)
					So(merchants, ShouldHaveLength, 1)

					accounts, err := onboarder.admin.Admin().ListServiceAccounts(ctx, tenant)
					So(err, ShouldBeNil)
					So(accounts, ShouldHaveLength, 1)

					subs, err := merchantClient.Webhooks().List(ctx)
					So(err, ShouldBeNil)
					So(subs, ShouldHaveLength, 1)

					types, err := merchantClient.Types().List(ctx, client.ListTypesOptions{
						InternalNames: []string{"product"},
					})
					So(err, ShouldBeNil)
					So(types.Items, ShouldHaveLength, 1)

					// The storefront is re-registered on every run. That is
					// deliberate: the registration is a PUT, so it repairs a
					// storefront that lost its merchant row.
					So(sf.registrationCount(), ShouldEqual, 2)
					reg, ok := sf.lastRegistration()
					So(ok, ShouldBeTrue)
					So(reg.Token, ShouldEqual, first.Token)
				})
			})

			Convey("When onboarding is re-run after the storefront failed part-way", func() {
				sf.failBackfills(true)
				_, err := onboarder.Onboard(ctx, in)
				So(err, ShouldNotBeNil)
				sf.failBackfills(false)

				repaired, err := onboarder.Onboard(ctx, in)

				Convey("Then the repair run completes with the same credential", func() {
					So(err, ShouldBeNil)
					So(repaired.Token, ShouldEqual, first.Token)
					So(sf.backfillCount(), ShouldBeGreaterThan, 1)
				})
			})

			Convey("When the same merchant id is pointed at a different tenant", func() {
				_, err := onboarder.Onboard(ctx, OnboardInput{
					ID: tenant, DisplayName: "Alpine Apparel", Tenant: newTenant("other"),
				})

				Convey("Then it is refused rather than orphaning the existing catalog", func() {
					So(err, ShouldNotBeNil)
					So(err.Error(), ShouldContainSubstring, "already bound")
				})
			})
		})
	})
}

// TestOnboardedMerchantsAreIsolated pins the tenancy model: each merchant is
// its own flexitype tenant, so one merchant's token cannot read another's
// catalog. It is why the storefront has to project rather than query across
// merchants.
func TestOnboardedMerchantsAreIsolated(t *testing.T) {
	Convey("Given two onboarded merchants", t, func() {
		ft := newFlexitype(t)
		store := newTestStore(t)
		sf := newFakeStorefront(t, "internal-token")
		onboarder := newOnboarder(t, store, sf, ft)
		ctx := context.Background()

		aTenant, bTenant := newTenant("alpine"), newTenant("bolt")
		a, err := onboarder.Onboard(ctx, OnboardInput{ID: aTenant, DisplayName: "Alpine", Tenant: aTenant})
		So(err, ShouldBeNil)
		b, err := onboarder.Onboard(ctx, OnboardInput{ID: bTenant, DisplayName: "Bolt", Tenant: bTenant})
		So(err, ShouldBeNil)

		aClient, err := client.New(ft.url, client.WithToken(a.Token))
		So(err, ShouldBeNil)
		bClient, err := client.New(ft.url, client.WithToken(b.Token))
		So(err, ShouldBeNil)

		Convey("When each merchant declares its own subtype", func() {
			aProduct := mustTypeID(t, aClient, "product")
			_, err := aClient.Types().Create(ctx, client.CreateTypeInput{
				InternalName: "apparel", DisplayName: "Apparel", ExtendsID: aProduct,
			})
			So(err, ShouldBeNil)

			bProduct := mustTypeID(t, bClient, "product")
			_, err = bClient.Types().Create(ctx, client.CreateTypeInput{
				InternalName: "electronics", DisplayName: "Electronics", ExtendsID: bProduct,
			})
			So(err, ShouldBeNil)

			Convey("Then neither merchant can see the other's types", func() {
				aTypes := typeNames(t, aClient)
				bTypes := typeNames(t, bClient)
				So(aTypes, ShouldContain, "apparel")
				So(aTypes, ShouldNotContain, "electronics")
				So(bTypes, ShouldContain, "electronics")
				So(bTypes, ShouldNotContain, "apparel")
			})

			Convey("Then each merchant owns its OWN copy of the root product type", func() {
				So(aProduct, ShouldNotEqual, bProduct)
			})
		})
	})
}

func mustTypeID(t *testing.T, c *client.Client, internalName string) string {
	t.Helper()
	page, err := c.Types().List(context.Background(), client.ListTypesOptions{InternalNames: []string{internalName}})
	if err != nil {
		t.Fatalf("list types: %v", err)
	}
	for _, item := range page.Items {
		if item.InternalName == internalName {
			return item.ID
		}
	}
	t.Fatalf("no type %q", internalName)
	return ""
}

func typeNames(t *testing.T, c *client.Client) []string {
	t.Helper()
	page, err := c.Types().List(context.Background(), client.ListTypesOptions{ListOptions: client.ListOptions{Limit: 200}})
	if err != nil {
		t.Fatalf("list types: %v", err)
	}
	out := []string{}
	for _, item := range page.Items {
		out = append(out, item.InternalName)
	}
	return out
}

// newOnboarderWithLog is newOnboarder with the log captured, so a test can
// assert on what was written.
func newOnboarderWithLog(t *testing.T, store *Store, sf *fakeStorefront, ft testFlexitype, sink io.Writer) *Onboarder {
	t.Helper()
	admin, err := client.New(ft.url, client.WithToken(ft.adminToken))
	if err != nil {
		t.Fatalf("build admin client: %v", err)
	}
	return NewOnboarder(store, admin, ft.url,
		NewStorefrontClient(sf.server.URL, "internal-token"),
		"http://storefront.test/hook",
		slog.New(slog.NewTextHandler(sink, nil)))
}
