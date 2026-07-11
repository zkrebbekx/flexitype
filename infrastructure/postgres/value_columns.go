package postgres

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// valueColumns is the polymorphic storage projection of a
// valueobjects.Value: one typed column per storage class.
type valueColumns struct {
	Bool  sql.NullBool    `db:"value_bool"`
	Int   sql.NullInt64   `db:"value_int"`
	Float sql.NullFloat64 `db:"value_float"`
	Text  sql.NullString  `db:"value_text"`
	Time  sql.NullTime    `db:"value_time"`
	JSON  []byte          `db:"value_json"`
}

// valueColumnName maps a data type to the column used by uniqueness probes.
func valueColumnName(dt valueobjects.DataType) string {
	switch dt {
	case valueobjects.DataTypeBool:
		return "value_bool"
	case valueobjects.DataTypeInteger:
		return "value_int"
	case valueobjects.DataTypeFloat:
		return "value_float"
	case valueobjects.DataTypeDate, valueobjects.DataTypeTime, valueobjects.DataTypeDateTime:
		return "value_time"
	case valueobjects.DataTypeJSON:
		return "value_json"
	default:
		return "value_text"
	}
}

// columnsFromValue projects a Value into its storage columns.
func columnsFromValue(v valueobjects.Value) valueColumns {
	var c valueColumns
	switch v.DataType() {
	case valueobjects.DataTypeBool:
		c.Bool = sql.NullBool{Bool: v.Bool(), Valid: true}
	case valueobjects.DataTypeInteger:
		c.Int = sql.NullInt64{Int64: v.Int(), Valid: true}
	case valueobjects.DataTypeFloat:
		c.Float = sql.NullFloat64{Float64: v.Float(), Valid: true}
	case valueobjects.DataTypeDate, valueobjects.DataTypeTime, valueobjects.DataTypeDateTime:
		c.Time = sql.NullTime{Time: v.Time(), Valid: true}
	case valueobjects.DataTypeJSON:
		c.JSON = v.JSON()
	default:
		c.Text = sql.NullString{String: v.Text(), Valid: true}
	}
	return c
}

// valueArg returns the driver argument matching valueColumnName for
// uniqueness probes.
func valueArg(v valueobjects.Value) any {
	switch v.DataType() {
	case valueobjects.DataTypeBool:
		return v.Bool()
	case valueobjects.DataTypeInteger:
		return v.Int()
	case valueobjects.DataTypeFloat:
		return v.Float()
	case valueobjects.DataTypeDate, valueobjects.DataTypeTime, valueobjects.DataTypeDateTime:
		return v.Time()
	case valueobjects.DataTypeJSON:
		// JSON travels as text: lib/pq maps []byte to bytea, which
		// PostgreSQL rejects for jsonb comparisons.
		return string(v.JSON())
	default:
		return v.Text()
	}
}

// valueFromColumns rebuilds a typed Value from its storage projection.
func valueFromColumns(dt valueobjects.DataType, c valueColumns) (valueobjects.Value, error) {
	switch dt {
	case valueobjects.DataTypeBool:
		return valueobjects.NewBoolValue(c.Bool.Bool), nil
	case valueobjects.DataTypeInteger:
		return valueobjects.NewIntegerValue(c.Int.Int64), nil
	case valueobjects.DataTypeFloat:
		return valueobjects.NewFloatValue(c.Float.Float64), nil
	case valueobjects.DataTypeString:
		return valueobjects.NewStringValue(c.Text.String), nil
	case valueobjects.DataTypeEnum:
		return valueobjects.NewEnumValue(c.Text.String), nil
	case valueobjects.DataTypeDecimal:
		return valueobjects.NewDecimalValue(c.Text.String)
	case valueobjects.DataTypeURL:
		return valueobjects.NewURLValue(c.Text.String)
	case valueobjects.DataTypeEmail:
		return valueobjects.NewEmailValue(c.Text.String)
	case valueobjects.DataTypeDate:
		return valueobjects.NewDateValue(c.Time.Time.UTC()), nil
	case valueobjects.DataTypeTime:
		return valueobjects.NewTimeValue(c.Time.Time.UTC()), nil
	case valueobjects.DataTypeDateTime:
		return valueobjects.NewDateTimeValue(c.Time.Time), nil
	case valueobjects.DataTypeJSON:
		return valueobjects.NewJSONValue(json.RawMessage(c.JSON))
	default:
		return valueobjects.Value{}, fmt.Errorf("unknown stored data type %q", dt)
	}
}

// nullableTime converts a *time.Time to its driver representation.
// nullableInt renders a *int as a NULL-aware SQL argument.
func nullableInt(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

func nullableTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// timePtr converts a scanned NullTime back to *time.Time.
func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
