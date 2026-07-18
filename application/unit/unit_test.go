package unit_test

import (
	"context"
	"errors"
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

// failingStore fails a chosen operation so the interactor's store-error
// passthrough is observable.
type failingStore struct {
	*memStore
	createErr error
	getErr    error
	listErr   error
	deleteErr error
	deleted   []string
}

func (s *failingStore) Create(ctx context.Context, f appunit.Family) error {
	if s.createErr != nil {
		return s.createErr
	}
	return s.memStore.Create(ctx, f)
}

func (s *failingStore) Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (appunit.Family, error) {
	if s.getErr != nil {
		return appunit.Family{}, s.getErr
	}
	return s.memStore.Get(ctx, tenant, id)
}

func (s *failingStore) List(ctx context.Context, tenant valueobjects.TenantID) ([]appunit.Family, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.memStore.List(ctx, tenant)
}

func (s *failingStore) Delete(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	s.deleted = append(s.deleted, id.String())
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.memStore.Delete(ctx, tenant, id)
}

func TestFamilyCreateValidation(t *testing.T) {
	Convey("Given a unit-family interactor over a tenant context", t, func() {
		store := newMemStore()
		i := appunit.NewInteractor(store)
		ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("acme"))

		Convey("When the name is empty", func() {
			f, err := i.Create(ctx, appunit.CreateInput{
				BaseUnit: "g", Units: map[string]float64{"g": 1},
			})

			Convey("Then creation is rejected and nothing is stored", func() {
				So(f, ShouldBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				list, lerr := i.List(ctx)
				So(lerr, ShouldBeNil)
				So(list, ShouldBeEmpty)
			})
		})

		Convey("When the base unit is empty", func() {
			f, err := i.Create(ctx, appunit.CreateInput{
				Name: "mass", Units: map[string]float64{"g": 1},
			})

			Convey("Then creation is rejected", func() {
				So(f, ShouldBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When no units are supplied", func() {
			f, err := i.Create(ctx, appunit.CreateInput{Name: "mass", BaseUnit: "g"})

			Convey("Then creation is rejected", func() {
				So(f, ShouldBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the base unit is present but not factor 1", func() {
			f, err := i.Create(ctx, appunit.CreateInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 2, "kg": 1000},
			})

			Convey("Then creation is rejected", func() {
				So(f, ShouldBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a factor is exactly zero", func() {
			f, err := i.Create(ctx, appunit.CreateInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "mg": 0},
			})

			Convey("Then creation is rejected", func() {
				So(f, ShouldBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a family is created", func() {
			f, err := i.Create(ctx, appunit.CreateInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
			})

			Convey("Then it is stamped with the context tenant and a fresh id", func() {
				So(err, ShouldBeNil)
				So(f.TenantID, ShouldEqual, valueobjects.TenantID("acme"))
				So(f.ID.String(), ShouldNotBeBlank)
				So(f.Name, ShouldEqual, "mass")
				So(f.BaseUnit, ShouldEqual, "g")
			})
		})

		Convey("When the store rejects the write", func() {
			boom := errors.New("store unavailable")
			failing := &failingStore{memStore: store, createErr: boom}
			f, err := appunit.NewInteractor(failing).Create(ctx, appunit.CreateInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1},
			})

			Convey("Then the store error surfaces and no family is returned", func() {
				So(f, ShouldBeNil)
				So(errors.Is(err, boom), ShouldBeTrue)
			})
		})
	})
}

func TestFamilyGetListDelete(t *testing.T) {
	Convey("Given a tenant with one stored unit family", t, func() {
		store := newMemStore()
		i := appunit.NewInteractor(store)
		ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("acme"))
		created, err := i.Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)

		Convey("When it is listed", func() {
			list, lerr := i.List(ctx)

			Convey("Then the tenant's family comes back", func() {
				So(lerr, ShouldBeNil)
				So(list, ShouldHaveLength, 1)
				So(list[0].ID.String(), ShouldEqual, created.ID.String())
			})
		})

		Convey("When another tenant lists families", func() {
			otherCtx := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			list, lerr := i.List(otherCtx)

			Convey("Then it sees none of this tenant's families", func() {
				So(lerr, ShouldBeNil)
				So(list, ShouldBeEmpty)
			})
		})

		Convey("When it is fetched with a malformed id", func() {
			f, gerr := i.Get(ctx, "not-a-ulid")

			Convey("Then the id is rejected as a validation error", func() {
				So(f, ShouldBeNil)
				So(domainerrors.IsValidation(gerr), ShouldBeTrue)
			})
		})

		Convey("When a well-formed but unknown id is fetched", func() {
			f, gerr := i.Get(ctx, ulid.New().String())

			Convey("Then it is a not-found error", func() {
				So(f, ShouldBeNil)
				So(domainerrors.IsNotFound(gerr), ShouldBeTrue)
			})
		})

		Convey("When it is fetched by another tenant", func() {
			otherCtx := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			f, gerr := i.Get(otherCtx, created.ID.String())

			Convey("Then the family is invisible across the tenant boundary", func() {
				So(f, ShouldBeNil)
				So(domainerrors.IsNotFound(gerr), ShouldBeTrue)
			})
		})

		Convey("When it is deleted and fetched again", func() {
			derr := i.Delete(ctx, created.ID.String())
			f, gerr := i.Get(ctx, created.ID.String())

			Convey("Then the delete succeeds and the family is gone", func() {
				So(derr, ShouldBeNil)
				So(f, ShouldBeNil)
				So(domainerrors.IsNotFound(gerr), ShouldBeTrue)
				list, lerr := i.List(ctx)
				So(lerr, ShouldBeNil)
				So(list, ShouldBeEmpty)
			})
		})

		Convey("When a nonexistent family is deleted", func() {
			derr := i.Delete(ctx, ulid.New().String())

			Convey("Then the delete is idempotent and the existing family survives", func() {
				So(derr, ShouldBeNil)
				list, lerr := i.List(ctx)
				So(lerr, ShouldBeNil)
				So(list, ShouldHaveLength, 1)
			})
		})

		Convey("When another tenant deletes the family", func() {
			otherCtx := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			derr := i.Delete(otherCtx, created.ID.String())

			Convey("Then the owning tenant still has it", func() {
				So(derr, ShouldBeNil)
				f, gerr := i.Get(ctx, created.ID.String())
				So(gerr, ShouldBeNil)
				So(f.ID.String(), ShouldEqual, created.ID.String())
			})
		})

		Convey("When a malformed id is deleted", func() {
			failing := &failingStore{memStore: store}
			derr := appunit.NewInteractor(failing).Delete(ctx, "nope")

			Convey("Then it is a validation error and the store is never called", func() {
				So(domainerrors.IsValidation(derr), ShouldBeTrue)
				So(failing.deleted, ShouldBeEmpty)
			})
		})

		Convey("When the store fails on delete", func() {
			boom := errors.New("store unavailable")
			failing := &failingStore{memStore: store, deleteErr: boom}
			derr := appunit.NewInteractor(failing).Delete(ctx, created.ID.String())

			Convey("Then the store error surfaces", func() {
				So(errors.Is(derr, boom), ShouldBeTrue)
			})
		})

		Convey("When the store fails on get", func() {
			boom := errors.New("store unavailable")
			failing := &failingStore{memStore: store, getErr: boom}
			f, gerr := appunit.NewInteractor(failing).Get(ctx, created.ID.String())

			Convey("Then the store error surfaces and no family is returned", func() {
				So(f, ShouldBeNil)
				So(errors.Is(gerr, boom), ShouldBeTrue)
			})
		})

		Convey("When the store fails on list", func() {
			boom := errors.New("store unavailable")
			failing := &failingStore{memStore: store, listErr: boom}
			list, lerr := appunit.NewInteractor(failing).List(ctx)

			Convey("Then the store error surfaces", func() {
				So(list, ShouldBeNil)
				So(errors.Is(lerr, boom), ShouldBeTrue)
			})
		})
	})
}
