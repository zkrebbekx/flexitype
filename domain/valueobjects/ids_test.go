package valueobjects

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

func TestIDParsing(t *testing.T) {
	Convey("Given the ULID-backed aggregate identifiers", t, func() {
		valid := ulid.New().String()

		Convey("When a well-formed ULID is parsed by each ID type", func() {
			Convey("Then the round-trip preserves the textual form", func() {
				td, err := ParseTypeDefinitionID(valid)
				So(err, ShouldBeNil)
				So(td.String(), ShouldEqual, valid)

				ad, err := ParseAttributeDefinitionID(valid)
				So(err, ShouldBeNil)
				So(ad.String(), ShouldEqual, valid)

				av, err := ParseAttributeValueID(valid)
				So(err, ShouldBeNil)
				So(av.String(), ShouldEqual, valid)

				dep, err := ParseDependencyID(valid)
				So(err, ShouldBeNil)
				So(dep.String(), ShouldEqual, valid)

				rd, err := ParseRelationshipDefinitionID(valid)
				So(err, ShouldBeNil)
				So(rd.String(), ShouldEqual, valid)

				rel, err := ParseRelationshipID(valid)
				So(err, ShouldBeNil)
				So(rel.String(), ShouldEqual, valid)
			})
		})

		Convey("When a malformed ULID is parsed", func() {
			Convey("Then each ID type names itself in the error", func() {
				_, err := ParseTypeDefinitionID("nope")
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "invalid type definition ID")

				_, err = ParseAttributeDefinitionID("nope")
				So(err.Error(), ShouldContainSubstring, "invalid attribute definition ID")

				_, err = ParseAttributeValueID("nope")
				So(err.Error(), ShouldContainSubstring, "invalid attribute value ID")

				_, err = ParseDependencyID("nope")
				So(err.Error(), ShouldContainSubstring, "invalid dependency ID")

				_, err = ParseRelationshipDefinitionID("nope")
				So(err.Error(), ShouldContainSubstring, "invalid relationship definition ID")

				_, err = ParseRelationshipID("nope")
				So(err.Error(), ShouldContainSubstring, "invalid relationship ID")
			})
		})

		Convey("When the Must variants are given a well-formed ULID", func() {
			Convey("Then they return the identifier without panicking", func() {
				So(MustParseTypeDefinitionID(valid).String(), ShouldEqual, valid)
				So(MustParseAttributeDefinitionID(valid).String(), ShouldEqual, valid)
				So(MustParseAttributeValueID(valid).String(), ShouldEqual, valid)
				So(MustParseDependencyID(valid).String(), ShouldEqual, valid)
				So(MustParseRelationshipDefinitionID(valid).String(), ShouldEqual, valid)
				So(MustParseRelationshipID(valid).String(), ShouldEqual, valid)
			})
		})

		Convey("When the Must variants are given garbage", func() {
			Convey("Then each panics rather than returning a zero identifier", func() {
				So(func() { MustParseTypeDefinitionID("nope") }, ShouldPanic)
				So(func() { MustParseAttributeDefinitionID("nope") }, ShouldPanic)
				So(func() { MustParseAttributeValueID("nope") }, ShouldPanic)
				So(func() { MustParseDependencyID("nope") }, ShouldPanic)
				So(func() { MustParseRelationshipDefinitionID("nope") }, ShouldPanic)
				So(func() { MustParseRelationshipID("nope") }, ShouldPanic)
			})
		})

		Convey("When identifiers are compared", func() {
			Convey("Then Equals is true only for the same underlying ULID", func() {
				So(MustParseTypeDefinitionID(valid).Equals(MustParseTypeDefinitionID(valid)), ShouldBeTrue)
				So(NewTypeDefinitionID().Equals(NewTypeDefinitionID()), ShouldBeFalse)

				So(MustParseAttributeDefinitionID(valid).Equals(MustParseAttributeDefinitionID(valid)), ShouldBeTrue)
				So(NewAttributeDefinitionID().Equals(NewAttributeDefinitionID()), ShouldBeFalse)

				So(MustParseAttributeValueID(valid).Equals(MustParseAttributeValueID(valid)), ShouldBeTrue)
				So(NewAttributeValueID().Equals(NewAttributeValueID()), ShouldBeFalse)

				So(MustParseDependencyID(valid).Equals(MustParseDependencyID(valid)), ShouldBeTrue)
				So(NewDependencyID().Equals(NewDependencyID()), ShouldBeFalse)

				So(MustParseRelationshipDefinitionID(valid).Equals(MustParseRelationshipDefinitionID(valid)), ShouldBeTrue)
				So(NewRelationshipDefinitionID().Equals(NewRelationshipDefinitionID()), ShouldBeFalse)

				So(MustParseRelationshipID(valid).Equals(MustParseRelationshipID(valid)), ShouldBeTrue)
				So(NewRelationshipID().Equals(NewRelationshipID()), ShouldBeFalse)
			})
		})
	})
}

func TestTenantID(t *testing.T) {
	Convey("Given consumer-supplied tenant identifiers", t, func() {
		Convey("When the tenant is omitted", func() {
			got, err := ParseTenantID("")

			Convey("Then it resolves to the default tenant rather than an empty one", func() {
				So(err, ShouldBeNil)
				So(got, ShouldEqual, DefaultTenant)
				So(got.IsZero(), ShouldBeFalse)
				So(got.String(), ShouldEqual, "default")
			})
		})

		Convey("When a well-formed tenant is supplied", func() {
			Convey("Then lowercase alphanumerics, dashes and underscores are accepted", func() {
				for _, s := range []string{"acme", "acme-eu", "acme_eu", "a", "t3nant", strings.Repeat("a", 64)} {
					got, err := ParseTenantID(s)
					So(err, ShouldBeNil)
					So(got.String(), ShouldEqual, s)
				}
			})
		})

		Convey("When a malformed tenant is supplied", func() {
			Convey("Then uppercase, leading punctuation, spaces and overlong names are rejected", func() {
				for _, s := range []string{"ACME", "-acme", "_acme", "acme corp", "acme.corp", strings.Repeat("a", 65)} {
					_, err := ParseTenantID(s)
					So(err, ShouldNotBeNil)
					So(err.Error(), ShouldContainSubstring, "invalid tenant ID")
				}
			})
		})

		Convey("When the zero tenant is inspected", func() {
			Convey("Then it reports itself as unset", func() {
				So(TenantID("").IsZero(), ShouldBeTrue)
			})
		})
	})
}

func TestEntityID(t *testing.T) {
	Convey("Given consumer-supplied entity identifiers", t, func() {
		Convey("When an ordinary identifier is parsed", func() {
			got, err := ParseEntityID("sku-1")

			Convey("Then it is passed through opaquely", func() {
				So(err, ShouldBeNil)
				So(got.String(), ShouldEqual, "sku-1")
				So(got.IsZero(), ShouldBeFalse)
			})
		})

		Convey("When the identifier is empty", func() {
			_, err := ParseEntityID("")

			Convey("Then it is rejected", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "must not be empty")
			})
		})

		Convey("When the identifier is at and beyond the length ceiling", func() {
			atLimit, err := ParseEntityID(strings.Repeat("x", 255))
			So(err, ShouldBeNil)
			_, overErr := ParseEntityID(strings.Repeat("x", 256))

			Convey("Then 255 characters are accepted and 256 are refused", func() {
				So(atLimit.String(), ShouldHaveLength, 255)
				So(overErr, ShouldNotBeNil)
				So(overErr.Error(), ShouldContainSubstring, "exceeds 255 characters")
			})
		})

		Convey("When the zero entity ID is inspected", func() {
			Convey("Then it reports itself as unset", func() {
				So(EntityID("").IsZero(), ShouldBeTrue)
			})
		})
	})
}

func TestScope(t *testing.T) {
	Convey("Given presentation scopes", t, func() {
		base := Scope{}
		auWeb := Scope{Locale: "en_AU", Channel: "web"}

		Convey("When a scope is tested for emptiness", func() {
			Convey("Then only a scope with neither locale nor channel is the base value", func() {
				So(base.IsZero(), ShouldBeTrue)
				So(Scope{Locale: "en_AU"}.IsZero(), ShouldBeFalse)
				So(Scope{Channel: "print"}.IsZero(), ShouldBeFalse)
				So(auWeb.IsZero(), ShouldBeFalse)
			})
		})

		Convey("When scopes are compared", func() {
			Convey("Then identity requires both dimensions to agree", func() {
				So(auWeb.Equals(Scope{Locale: "en_AU", Channel: "web"}), ShouldBeTrue)
				So(auWeb.Equals(Scope{Locale: "en_AU", Channel: "print"}), ShouldBeFalse)
				So(auWeb.Equals(Scope{Locale: "de_DE", Channel: "web"}), ShouldBeFalse)
				So(auWeb.Equals(base), ShouldBeFalse)
				So(base.Equals(Scope{}), ShouldBeTrue)
			})
		})
	})
}

func TestDataTypeClassification(t *testing.T) {
	Convey("Given the supported data types", t, func() {
		Convey("When an unknown name is parsed", func() {
			_, err := ParseDataType("colour")

			Convey("Then it is rejected", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `unknown data type "colour"`)
			})
		})

		Convey("When every known name is parsed", func() {
			Convey("Then it round-trips through String", func() {
				for _, dt := range []DataType{
					DataTypeBool, DataTypeString, DataTypeInteger, DataTypeFloat, DataTypeDecimal,
					DataTypeDate, DataTypeTime, DataTypeDateTime, DataTypeEnum, DataTypeURL,
					DataTypeEmail, DataTypeJSON, DataTypeMedia, DataTypeQuantity,
				} {
					got, err := ParseDataType(dt.String())
					So(err, ShouldBeNil)
					So(got, ShouldEqual, dt)
				}
			})
		})

		Convey("When types are classified", func() {
			textual := map[DataType]bool{DataTypeString: true, DataTypeEnum: true, DataTypeURL: true, DataTypeEmail: true}
			ordered := map[DataType]bool{
				DataTypeInteger: true, DataTypeFloat: true, DataTypeDecimal: true,
				DataTypeDate: true, DataTypeTime: true, DataTypeDateTime: true, DataTypeQuantity: true,
			}
			temporal := map[DataType]bool{DataTypeDate: true, DataTypeTime: true, DataTypeDateTime: true}

			Convey("Then textual, ordered and temporal partitions match the constraint rules they gate", func() {
				for _, dt := range []DataType{
					DataTypeBool, DataTypeString, DataTypeInteger, DataTypeFloat, DataTypeDecimal,
					DataTypeDate, DataTypeTime, DataTypeDateTime, DataTypeEnum, DataTypeURL,
					DataTypeEmail, DataTypeJSON, DataTypeMedia, DataTypeQuantity,
				} {
					So(dt.IsTextual(), ShouldEqual, textual[dt])
					So(dt.IsOrdered(), ShouldEqual, ordered[dt])
					So(dt.IsTemporal(), ShouldEqual, temporal[dt])
				}
			})

			Convey("And an unknown type belongs to no partition", func() {
				unknown := DataType("colour")
				So(unknown.IsTextual(), ShouldBeFalse)
				So(unknown.IsOrdered(), ShouldBeFalse)
				So(unknown.IsTemporal(), ShouldBeFalse)
			})
		})
	})
}
