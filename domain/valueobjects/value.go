package valueobjects

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

// Wire formats for temporal values.
const (
	DateFormat = "2006-01-02"
	TimeFormat = "15:04:05"
)

const (
	maxURLLength     = 2048
	maxEmailLength   = 320
	maxDecimalLength = 40
)

var decimalPattern = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)

// Value is an immutable, strongly-typed attribute value: a tagged union
// across every DataType. Construct through the typed constructors or
// ParseValue; the zero Value means "absent".
type Value struct {
	dataType DataType
	boolVal  bool
	intVal   int64
	floatVal float64
	textVal  string
	timeVal  time.Time
	jsonVal  json.RawMessage
}

// NewBoolValue builds a bool value.
func NewBoolValue(b bool) Value {
	return Value{dataType: DataTypeBool, boolVal: b}
}

// NewStringValue builds a string value.
func NewStringValue(s string) Value {
	return Value{dataType: DataTypeString, textVal: s}
}

// NewEnumValue builds an enum member value. Membership of the allowed set
// is enforced by the attribute definition's OneOf constraint.
func NewEnumValue(s string) Value {
	return Value{dataType: DataTypeEnum, textVal: s}
}

// NewIntegerValue builds an integer value.
func NewIntegerValue(i int64) Value {
	return Value{dataType: DataTypeInteger, intVal: i}
}

// NewFloatValue builds a float value.
func NewFloatValue(f float64) Value {
	return Value{dataType: DataTypeFloat, floatVal: f}
}

// NewDecimalValue builds an arbitrary-precision decimal from its canonical
// string form, e.g. "1234.560000".
func NewDecimalValue(s string) (Value, error) {
	if len(s) > maxDecimalLength || !decimalPattern.MatchString(s) {
		return Value{}, fmt.Errorf("invalid decimal %q", s)
	}
	return Value{dataType: DataTypeDecimal, textVal: s}, nil
}

// NewDateValue builds a date value; the time-of-day portion is discarded.
func NewDateValue(t time.Time) Value {
	y, m, d := t.Date()
	return Value{dataType: DataTypeDate, timeVal: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// NewTimeValue builds a time-of-day value; the date portion is discarded.
func NewTimeValue(t time.Time) Value {
	return Value{dataType: DataTypeTime, timeVal: time.Date(0, 1, 1, t.Hour(), t.Minute(), t.Second(), 0, time.UTC)}
}

// NewDateTimeValue builds a datetime value in UTC.
func NewDateTimeValue(t time.Time) Value {
	return Value{dataType: DataTypeDateTime, timeVal: t.UTC()}
}

// NewURLValue builds a validated absolute URL value.
func NewURLValue(s string) (Value, error) {
	if len(s) > maxURLLength {
		return Value{}, fmt.Errorf("url exceeds %d characters", maxURLLength)
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Value{}, fmt.Errorf("invalid url %q: must be absolute", s)
	}
	return Value{dataType: DataTypeURL, textVal: s}, nil
}

// NewEmailValue builds a validated email address value.
func NewEmailValue(s string) (Value, error) {
	if len(s) > maxEmailLength {
		return Value{}, fmt.Errorf("email exceeds %d characters", maxEmailLength)
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return Value{}, fmt.Errorf("invalid email %q", s)
	}
	return Value{dataType: DataTypeEmail, textVal: s}, nil
}

// NewJSONValue builds a JSON document value, stored in compact form.
func NewJSONValue(raw json.RawMessage) (Value, error) {
	if !json.Valid(raw) {
		return Value{}, fmt.Errorf("invalid json document")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return Value{}, fmt.Errorf("compact json: %w", err)
	}
	return Value{dataType: DataTypeJSON, jsonVal: json.RawMessage(buf.Bytes())}, nil
}

// ParseValue decodes a raw JSON scalar into a typed Value according to the
// declared data type. This is the single entry point for values arriving
// over the API.
func ParseValue(dt DataType, raw json.RawMessage) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	switch dt {
	case DataTypeBool:
		var b bool
		if err := dec.Decode(&b); err != nil {
			return Value{}, fmt.Errorf("expected boolean: %w", err)
		}
		return NewBoolValue(b), nil

	case DataTypeString:
		s, err := decodeString(dec)
		if err != nil {
			return Value{}, err
		}
		return NewStringValue(s), nil

	case DataTypeEnum:
		s, err := decodeString(dec)
		if err != nil {
			return Value{}, err
		}
		if s == "" {
			return Value{}, fmt.Errorf("enum value must not be empty")
		}
		return NewEnumValue(s), nil

	case DataTypeInteger:
		var n json.Number
		if err := dec.Decode(&n); err != nil {
			return Value{}, fmt.Errorf("expected integer: %w", err)
		}
		i, err := n.Int64()
		if err != nil {
			return Value{}, fmt.Errorf("expected integer, got %q", n.String())
		}
		return NewIntegerValue(i), nil

	case DataTypeFloat:
		var n json.Number
		if err := dec.Decode(&n); err != nil {
			return Value{}, fmt.Errorf("expected number: %w", err)
		}
		f, err := n.Float64()
		if err != nil {
			return Value{}, fmt.Errorf("expected number, got %q", n.String())
		}
		return NewFloatValue(f), nil

	case DataTypeDecimal:
		// Accept both "12.50" (preferred: no float rounding) and 12.50.
		var decoded interface{}
		if err := dec.Decode(&decoded); err != nil {
			return Value{}, fmt.Errorf("expected decimal: %w", err)
		}
		switch v := decoded.(type) {
		case string:
			return NewDecimalValue(v)
		case json.Number:
			return NewDecimalValue(v.String())
		default:
			return Value{}, fmt.Errorf("expected decimal string or number")
		}

	case DataTypeDate:
		s, err := decodeString(dec)
		if err != nil {
			return Value{}, err
		}
		t, err := time.Parse(DateFormat, s)
		if err != nil {
			return Value{}, fmt.Errorf("expected date %q: %w", DateFormat, err)
		}
		return NewDateValue(t), nil

	case DataTypeTime:
		s, err := decodeString(dec)
		if err != nil {
			return Value{}, err
		}
		t, err := time.Parse(TimeFormat, s)
		if err != nil {
			return Value{}, fmt.Errorf("expected time %q: %w", TimeFormat, err)
		}
		return NewTimeValue(t), nil

	case DataTypeDateTime:
		s, err := decodeString(dec)
		if err != nil {
			return Value{}, err
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return Value{}, fmt.Errorf("expected RFC 3339 datetime: %w", err)
		}
		return NewDateTimeValue(t), nil

	case DataTypeURL:
		s, err := decodeString(dec)
		if err != nil {
			return Value{}, err
		}
		return NewURLValue(s)

	case DataTypeEmail:
		s, err := decodeString(dec)
		if err != nil {
			return Value{}, err
		}
		return NewEmailValue(s)

	case DataTypeJSON:
		return NewJSONValue(raw)

	default:
		return Value{}, fmt.Errorf("unknown data type %q", dt)
	}
}

func decodeString(dec *json.Decoder) (string, error) {
	var s string
	if err := dec.Decode(&s); err != nil {
		return "", fmt.Errorf("expected string: %w", err)
	}
	return s, nil
}

// DataType returns the value's declared type.
func (v Value) DataType() DataType { return v.dataType }

// IsZero reports whether the value is absent.
func (v Value) IsZero() bool { return v.dataType == "" }

// Bool returns the boolean payload (bool values only).
func (v Value) Bool() bool { return v.boolVal }

// Int returns the integer payload (integer values only).
func (v Value) Int() int64 { return v.intVal }

// Float returns the float payload (float values only).
func (v Value) Float() float64 { return v.floatVal }

// Text returns the textual payload (string, enum, url, email and decimal).
func (v Value) Text() string { return v.textVal }

// Time returns the temporal payload (date, time and datetime values).
func (v Value) Time() time.Time { return v.timeVal }

// JSON returns the raw document payload (json values only).
func (v Value) JSON() json.RawMessage { return v.jsonVal }

// Length is the rune count of textual values, used by length constraints.
func (v Value) Length() int { return utf8.RuneCountInString(v.textVal) }

// String renders the value for humans and logs.
func (v Value) String() string {
	switch v.dataType {
	case DataTypeBool:
		return strconv.FormatBool(v.boolVal)
	case DataTypeInteger:
		return strconv.FormatInt(v.intVal, 10)
	case DataTypeFloat:
		return strconv.FormatFloat(v.floatVal, 'g', -1, 64)
	case DataTypeDate:
		return v.timeVal.Format(DateFormat)
	case DataTypeTime:
		return v.timeVal.Format(TimeFormat)
	case DataTypeDateTime:
		return v.timeVal.Format(time.RFC3339Nano)
	case DataTypeJSON:
		return string(v.jsonVal)
	default:
		return v.textVal
	}
}

// MarshalJSON renders the natural JSON form of the value.
func (v Value) MarshalJSON() ([]byte, error) {
	switch v.dataType {
	case DataTypeBool:
		return json.Marshal(v.boolVal)
	case DataTypeInteger:
		return json.Marshal(v.intVal)
	case DataTypeFloat:
		return json.Marshal(v.floatVal)
	case DataTypeJSON:
		return v.jsonVal, nil
	case "":
		return []byte("null"), nil
	default:
		return json.Marshal(v.String())
	}
}

// typedValueJSON is the self-describing persisted form of a Value, used
// wherever a value must round-trip through storage without external type
// context (defaults, constraint operands, dependency conditions).
type typedValueJSON struct {
	Type  DataType        `json:"type"`
	Value json.RawMessage `json:"value"`
}

// MarshalTyped encodes the value with its data type so it can be decoded
// without external context.
func (v Value) MarshalTyped() (json.RawMessage, error) {
	if v.IsZero() {
		return json.RawMessage("null"), nil
	}
	natural, err := v.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return json.Marshal(typedValueJSON{Type: v.dataType, Value: natural})
}

// UnmarshalTypedValue decodes the self-describing form written by
// MarshalTyped.
func UnmarshalTypedValue(raw json.RawMessage) (Value, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return Value{}, nil
	}
	var w typedValueJSON
	if err := json.Unmarshal(raw, &w); err != nil {
		return Value{}, fmt.Errorf("decode typed value: %w", err)
	}
	return ParseValue(w.Type, w.Value)
}

// Equal reports whether two values have the same type and content. Decimals
// compare numerically ("1.50" equals "1.5").
func (v Value) Equal(other Value) bool {
	if v.dataType != other.dataType {
		return false
	}
	switch v.dataType {
	case DataTypeDecimal:
		cmp, err := v.Compare(other)
		return err == nil && cmp == 0
	case DataTypeJSON:
		return bytes.Equal(v.jsonVal, other.jsonVal)
	case DataTypeDate, DataTypeTime, DataTypeDateTime:
		return v.timeVal.Equal(other.timeVal)
	case DataTypeBool:
		return v.boolVal == other.boolVal
	case DataTypeInteger:
		return v.intVal == other.intVal
	case DataTypeFloat:
		return v.floatVal == other.floatVal
	default:
		return v.textVal == other.textVal
	}
}

// Compare orders two values of the same ordered data type (-1, 0, 1).
func (v Value) Compare(other Value) (int, error) {
	if v.dataType != other.dataType {
		return 0, fmt.Errorf("cannot compare %s with %s", v.dataType, other.dataType)
	}
	switch v.dataType {
	case DataTypeInteger:
		return compareOrdered(v.intVal, other.intVal), nil
	case DataTypeFloat:
		return compareOrdered(v.floatVal, other.floatVal), nil
	case DataTypeDecimal:
		a, ok := new(big.Rat).SetString(v.textVal)
		if !ok {
			return 0, fmt.Errorf("corrupt decimal %q", v.textVal)
		}
		b, ok := new(big.Rat).SetString(other.textVal)
		if !ok {
			return 0, fmt.Errorf("corrupt decimal %q", other.textVal)
		}
		return a.Cmp(b), nil
	case DataTypeDate, DataTypeTime, DataTypeDateTime:
		return v.timeVal.Compare(other.timeVal), nil
	default:
		return 0, fmt.Errorf("data type %s is not ordered", v.dataType)
	}
}

func compareOrdered[T int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
