package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// matchesFixture seeds one person whose name spans two attributes.
type matchesFixture struct {
	ctx    context.Context
	svc    *flexitype.Service
	typeID string
}

func newMatchesFixture(t *testing.T, svc *flexitype.Service) matchesFixture {
	t.Helper()
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	ia := svc.Interactors(ctx)

	person, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "person", DisplayName: "Person",
	})
	So(err, ShouldBeNil)
	for _, name := range []string{"first_name", "last_name", "secret_note"} {
		_, aerr := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: person.ID.String(), InternalName: name,
			DisplayName: name, DataType: "string",
		})
		So(aerr, ShouldBeNil)
	}
	_, err = ia.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: person.ID.String(), InternalName: "qty",
		DisplayName: "Qty", DataType: "integer",
	})
	So(err, ShouldBeNil)
	attrs, err := ia.TypeDefinitions().EffectiveAttributes(ctx, person.ID.String())
	So(err, ShouldBeNil)
	set := func(name, value string) {
		for _, a := range attrs {
			if a.Attribute.InternalName == name {
				raw, _ := json.Marshal(value)
				_, werr := ia.Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: a.Attribute.ID.String(), EntityID: "e1",
					TypeDefinitionID: person.ID.String(), Value: raw,
				})
				So(werr, ShouldBeNil)
			}
		}
	}
	set("first_name", "alice")
	set("last_name", "smith")
	set("secret_note", "confidential")
	for _, a := range attrs {
		if a.Attribute.InternalName == "qty" {
			raw, _ := json.Marshal(424242)
			_, werr := ia.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: a.Attribute.ID.String(), EntityID: "e1",
				TypeDefinitionID: person.ID.String(), Value: raw,
			})
			So(werr, ShouldBeNil)
		}
	}
	return matchesFixture{ctx: ctx, svc: svc, typeID: person.ID.String()}
}

func (f matchesFixture) run(ctx context.Context, q string) []string {
	out, err := f.svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
		Type: "person", Query: q, Page: db.PageArgs{},
	})
	So(err, ShouldBeNil)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, item.EntityID)
	}
	return ids
}

// TestMatchesAcrossTwoAttributes covers issue #586.
//
// The restricted search probes the PER-ATTRIBUTE vectors, and each holds one
// attribute's lexemes. plainto_tsquery ANDs the words, so probing them
// individually asked for every word in a single attribute: matches("alice
// smith") returned nothing when first_name held "alice" and last_name held
// "smith". The in-memory backend joins the readable values into one document,
// so it matched — the two backends answered differently, and only Postgres was
// wrong.
func TestMatchesAcrossTwoAttributes(t *testing.T) {
	restricted := func(ctx context.Context) context.Context {
		return uow.WithAccess(ctx, uow.Access{
			Attr: map[string]uow.Perm{
				"first_name": uow.PermRead, "last_name": uow.PermRead,
				"secret_note": uow.PermNone,
			},
			Default: uow.PermNone,
		})
	}

	Convey("Given a person whose name spans two readable attributes (memory)", t, func() {
		f := newMatchesFixture(t, flexitype.NewInMemory(flexitype.WithSearchIndex()))

		Convey("Then an admin finds them", func() {
			So(f.run(f.ctx, `matches("alice smith")`), ShouldResemble, []string{"e1"})
		})

		Convey("Then a restricted principal finds them too", func() {
			So(f.run(restricted(f.ctx), `matches("alice smith")`), ShouldResemble, []string{"e1"})
		})

		Convey("Then a restricted principal cannot reach a hidden attribute", func() {
			So(f.run(restricted(f.ctx), `matches("confidential")`), ShouldBeEmpty)
		})
	})
}

// TestMatchesAcrossTwoAttributesPostgres is the same matrix against the
// backend that was wrong. The divergence is the point, so both run.
func TestMatchesAcrossTwoAttributesPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a person whose name spans two readable attributes (Postgres)", t, func() {
		truncateAll(t, pool, svc)
		f := newMatchesFixture(t, svc)
		restricted := uow.WithAccess(f.ctx, uow.Access{
			Attr: map[string]uow.Perm{
				"first_name": uow.PermRead, "last_name": uow.PermRead,
				"secret_note": uow.PermNone,
			},
			Default: uow.PermNone,
		})

		Convey("Then an admin finds them", func() {
			So(f.run(f.ctx, `matches("alice smith")`), ShouldResemble, []string{"e1"})
		})

		Convey("Then a restricted principal finds them too", func() {
			// This returned nothing before: no single per-attribute vector
			// held both words.
			So(f.run(restricted, `matches("alice smith")`), ShouldResemble, []string{"e1"})
		})

		Convey("Then one word in one attribute still matches", func() {
			So(f.run(restricted, `matches("alice")`), ShouldResemble, []string{"e1"})
		})

		Convey("Then a word in NO readable attribute does not match", func() {
			So(f.run(restricted, `matches("confidential")`), ShouldBeEmpty)
		})

		Convey("Then a readable word plus a hidden one does not match", func() {
			// The hidden word must not be satisfiable from a restricted
			// attribute, or the query becomes an oracle over it.
			So(f.run(restricted, `matches("alice confidential")`), ShouldBeEmpty)
		})

		Convey("Then a word that appears nowhere does not match", func() {
			So(f.run(restricted, `matches("nobody")`), ShouldBeEmpty)
		})
	})
}

// TestMatchesReachIsTheSameForEveryPrincipal covers issue #601.
//
// The entity-level vector has always indexed textual values only. The
// per-attribute vectors indexed every rendering, including numbers — so a
// field-restricted principal could find an entity by an integer that an admin
// could not. Nothing leaked, because the hit was on a readable attribute; the
// privilege simply ran backwards, which is its own kind of wrong.
func TestMatchesReachIsTheSameForEveryPrincipal(t *testing.T) {
	restrictedAccess := uow.Access{
		Attr: map[string]uow.Perm{
			"first_name": uow.PermRead, "last_name": uow.PermRead,
			"qty": uow.PermRead, "secret_note": uow.PermNone,
		},
		Default: uow.PermNone,
	}

	Convey("Given an entity with an integer value (memory)", t, func() {
		f := newMatchesFixture(t, flexitype.NewInMemory(flexitype.WithSearchIndex()))
		restricted := uow.WithAccess(f.ctx, restrictedAccess)

		Convey("Then both principals agree on a non-textual value", func() {
			So(f.run(f.ctx, `matches("424242")`), ShouldResemble, f.run(restricted, `matches("424242")`))
		})

		Convey("Then a textual value still matches for both", func() {
			So(f.run(f.ctx, `matches("alice")`), ShouldResemble, []string{"e1"})
			So(f.run(restricted, `matches("alice")`), ShouldResemble, []string{"e1"})
		})
	})
}

// TestMatchesReachIsTheSameForEveryPrincipalPostgres is the same on the real
// backend.
func TestMatchesReachIsTheSameForEveryPrincipalPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given an entity with an integer value (Postgres)", t, func() {
		truncateAll(t, pool, svc)
		f := newMatchesFixture(t, svc)
		restricted := uow.WithAccess(f.ctx, uow.Access{
			Attr: map[string]uow.Perm{
				"first_name": uow.PermRead, "last_name": uow.PermRead,
				"qty": uow.PermRead, "secret_note": uow.PermNone,
			},
			Default: uow.PermNone,
		})

		Convey("Then both principals agree on a non-textual value", func() {
			So(f.run(f.ctx, `matches("424242")`), ShouldResemble, f.run(restricted, `matches("424242")`))
		})

		Convey("Then a textual value still matches for both", func() {
			So(f.run(restricted, `matches("smith")`), ShouldResemble, []string{"e1"})
		})
	})
}
