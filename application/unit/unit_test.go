package unit_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// memStore is a minimal in-memory unit-family store for the usecase tests.
type memStore struct{ families map[string]appunit.Family }

func newMemStore() *memStore { return &memStore{families: map[string]appunit.Family{}} }

func (s *memStore) Create(_ context.Context, f appunit.Family) error {
	s.families[f.ID.String()] = f
	return nil
}

func (s *memStore) Get(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) (appunit.Family, error) {
	f, ok := s.families[id.String()]
	if !ok || f.TenantID != tenant {
		return appunit.Family{}, domainerrors.NewNotFound("unit_family", id.String())
	}
	return f, nil
}

func (s *memStore) List(_ context.Context, tenant valueobjects.TenantID) ([]appunit.Family, error) {
	out := []appunit.Family{}
	for _, f := range s.families {
		if f.TenantID == tenant {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *memStore) Delete(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	if f, ok := s.families[id.String()]; ok && f.TenantID == tenant {
		delete(s.families, id.String())
	}
	return nil
}

func TestFamilyConversion(t *testing.T) {
	Convey("Given a mass family with base gram and kilogram factor 1000", t, func() {
		fam := appunit.Family{
			Name:     "mass",
			BaseUnit: "g",
			Units:    map[string]float64{"g": 1, "kg": 1000},
		}

		Convey("When converting a kilogram magnitude to the base", func() {
			base, err := fam.ToBase("5", "kg")

			Convey("Then it yields the gram-equivalent magnitude", func() {
				So(err, ShouldBeNil)
				So(base, ShouldEqual, 5000)
			})
		})

		Convey("When converting a gram magnitude to the base", func() {
			base, err := fam.ToBase("6000", "g")

			Convey("Then the base is unchanged", func() {
				So(err, ShouldBeNil)
				So(base, ShouldEqual, 6000)
			})
		})

		Convey("When the unit is not a member of the family", func() {
			_, err := fam.ToBase("10", "mm")

			Convey("Then conversion is a validation error", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the magnitude is not numeric", func() {
			_, err := fam.ToBase("heavy", "kg")

			Convey("Then conversion is a validation error", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}

func TestFamilyCreate(t *testing.T) {
	Convey("Given a unit-family interactor over a tenant context", t, func() {
		store := newMemStore()
		i := appunit.NewInteractor(store)
		ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("acme"))

		Convey("When creating a well-formed family", func() {
			f, err := i.Create(ctx, appunit.CreateInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
			})

			Convey("Then it persists and is retrievable", func() {
				So(err, ShouldBeNil)
				So(f, ShouldNotBeNil)
				got, gerr := i.Get(ctx, f.ID.String())
				So(gerr, ShouldBeNil)
				So(got.BaseUnit, ShouldEqual, "g")
				So(got.Units["kg"], ShouldEqual, 1000)
			})
		})

		Convey("When the base unit is absent or not factor 1", func() {
			_, err := i.Create(ctx, appunit.CreateInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"kg": 1000},
			})

			Convey("Then creation is rejected", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a factor is non-positive", func() {
			_, err := i.Create(ctx, appunit.CreateInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": -5},
			})

			Convey("Then creation is rejected", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}
