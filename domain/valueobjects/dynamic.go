package valueobjects

import (
	"encoding/json"
	"fmt"
	"time"
)

// DynamicKind enumerates the runtime-computed value kinds.
type DynamicKind string

const (
	// DynamicNow resolves to the current instant (datetime attributes).
	DynamicNow DynamicKind = "now"
	// DynamicToday resolves to the current date (date attributes).
	DynamicToday DynamicKind = "today"
	// DynamicRelative resolves to now offset by Amount * Period.
	DynamicRelative DynamicKind = "relative_time"
)

// RelativePeriod is the unit for DynamicRelative offsets.
type RelativePeriod string

// The supported relative periods.
const (
	PeriodSeconds RelativePeriod = "seconds"
	PeriodMinutes RelativePeriod = "minutes"
	PeriodHours   RelativePeriod = "hours"
	PeriodDays    RelativePeriod = "days"
	PeriodWeeks   RelativePeriod = "weeks"
)

var periodDurations = map[RelativePeriod]time.Duration{
	PeriodSeconds: time.Second,
	PeriodMinutes: time.Minute,
	PeriodHours:   time.Hour,
	PeriodDays:    24 * time.Hour,
	PeriodWeeks:   7 * 24 * time.Hour,
}

// DynamicValue is a value computed at evaluation time rather than stored:
// "now", "today", or "now plus/minus an offset". Used for temporal defaults
// and dynamic dependency conditions.
type DynamicValue struct {
	Kind   DynamicKind    `json:"kind"`
	Period RelativePeriod `json:"period,omitempty"`
	Amount int            `json:"amount,omitempty"`
}

// Validate checks the dynamic value's shape and its compatibility with the
// attribute's data type.
func (d DynamicValue) Validate(dt DataType) error {
	if !dt.IsTemporal() {
		return fmt.Errorf("dynamic values require a temporal data type, got %s", dt)
	}
	switch d.Kind {
	case DynamicNow, DynamicToday:
		if d.Period != "" || d.Amount != 0 {
			return fmt.Errorf("%s does not accept period or amount", d.Kind)
		}
		return nil
	case DynamicRelative:
		if _, ok := periodDurations[d.Period]; !ok {
			return fmt.Errorf("unknown relative period %q", d.Period)
		}
		return nil
	default:
		return fmt.Errorf("unknown dynamic value kind %q", d.Kind)
	}
}

// Resolve computes the concrete Value for the given data type at instant
// now.
func (d DynamicValue) Resolve(dt DataType, now time.Time) (Value, error) {
	var t time.Time
	switch d.Kind {
	case DynamicNow, DynamicToday:
		t = now
	case DynamicRelative:
		dur, ok := periodDurations[d.Period]
		if !ok {
			return Value{}, fmt.Errorf("unknown relative period %q", d.Period)
		}
		t = now.Add(time.Duration(d.Amount) * dur)
	default:
		return Value{}, fmt.Errorf("unknown dynamic value kind %q", d.Kind)
	}

	switch dt {
	case DataTypeDate:
		return NewDateValue(t), nil
	case DataTypeTime:
		return NewTimeValue(t), nil
	case DataTypeDateTime:
		return NewDateTimeValue(t), nil
	default:
		return Value{}, fmt.Errorf("dynamic values require a temporal data type, got %s", dt)
	}
}

// Default declares an attribute's default: either a static typed value or a
// dynamic one resolved when the default is applied. Exactly one side is set.
type Default struct {
	Static  *Value
	Dynamic *DynamicValue
}

type defaultJSON struct {
	Static  json.RawMessage `json:"static,omitempty"`
	Dynamic *DynamicValue   `json:"dynamic,omitempty"`
}

// MarshalJSON persists the static side in its self-describing typed form so
// the default survives storage round-trips.
func (d Default) MarshalJSON() ([]byte, error) {
	out := defaultJSON{Dynamic: d.Dynamic}
	if d.Static != nil {
		typed, err := d.Static.MarshalTyped()
		if err != nil {
			return nil, err
		}
		out.Static = typed
	}
	return json.Marshal(out)
}

// UnmarshalJSON is the inverse of MarshalJSON.
func (d *Default) UnmarshalJSON(b []byte) error {
	var in defaultJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	d.Dynamic = in.Dynamic
	d.Static = nil
	if len(in.Static) > 0 && string(in.Static) != "null" {
		v, err := UnmarshalTypedValue(in.Static)
		if err != nil {
			return err
		}
		d.Static = &v
	}
	return nil
}

// Validate checks the default's shape against the attribute's data type.
func (d Default) Validate(dt DataType) error {
	switch {
	case d.Static != nil && d.Dynamic != nil:
		return fmt.Errorf("default must be static or dynamic, not both")
	case d.Static != nil:
		if d.Static.DataType() != dt {
			return fmt.Errorf("default value type %s does not match attribute type %s", d.Static.DataType(), dt)
		}
		return nil
	case d.Dynamic != nil:
		return d.Dynamic.Validate(dt)
	default:
		return fmt.Errorf("default must set static or dynamic")
	}
}

// Resolve produces the concrete default Value at instant now.
func (d Default) Resolve(dt DataType, now time.Time) (Value, error) {
	if d.Static != nil {
		return *d.Static, nil
	}
	if d.Dynamic != nil {
		return d.Dynamic.Resolve(dt, now)
	}
	return Value{}, fmt.Errorf("empty default")
}
