// Package ulid wraps oklog/ulid/v2 behind a small, strongly-typed ID that
// knows how to travel through JSON, SQL and text encodings. Domain packages
// embed ID inside their own newtypes (see domain/valueobjects) so identifiers
// of different aggregates can never be mixed up at compile time.
package ulid

import (
	"crypto/rand"
	"database/sql/driver"
	"fmt"
	"sync"
	"time"

	oulid "github.com/oklog/ulid/v2"
)

// ID is a ULID: lexicographically sortable, timestamp-prefixed and
// collision-resistant. The zero value is treated as "absent" and maps to
// SQL NULL.
type ID struct {
	u oulid.ULID
}

var (
	entropyMu sync.Mutex
	entropy   = oulid.Monotonic(rand.Reader, 0)
)

// New generates a new ID. Generation is safe for concurrent use and
// monotonic within the same millisecond.
func New() ID {
	entropyMu.Lock()
	defer entropyMu.Unlock()
	return ID{u: oulid.MustNew(oulid.Timestamp(time.Now()), entropy)}
}

// Parse parses the canonical 26-character ULID string form.
func Parse(s string) (ID, error) {
	u, err := oulid.ParseStrict(s)
	if err != nil {
		return ID{}, fmt.Errorf("parse ulid %q: %w", s, err)
	}
	return ID{u: u}, nil
}

// MustParse parses s and panics on failure. Reserve for tests and constants.
func MustParse(s string) ID {
	id, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return id
}

// String returns the canonical 26-character representation.
func (id ID) String() string {
	return id.u.String()
}

// IsZero reports whether the ID is the zero (absent) value.
func (id ID) IsZero() bool {
	return id.u == oulid.ULID{}
}

// Time returns the timestamp encoded in the ID.
func (id ID) Time() time.Time {
	return oulid.Time(id.u.Time())
}

// Compare lexicographically compares two IDs (-1, 0, 1).
func (id ID) Compare(other ID) int {
	return id.u.Compare(other.u)
}

// Value implements driver.Valuer. Zero IDs map to NULL.
func (id ID) Value() (driver.Value, error) {
	if id.IsZero() {
		return nil, nil
	}
	return id.String(), nil
}

// Scan implements sql.Scanner, accepting NULL, string and []byte.
func (id *ID) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*id = ID{}
		return nil
	case string:
		if v == "" {
			*id = ID{}
			return nil
		}
		parsed, err := Parse(v)
		if err != nil {
			return err
		}
		*id = parsed
		return nil
	case []byte:
		return id.Scan(string(v))
	default:
		return fmt.Errorf("cannot scan %T into ulid.ID", src)
	}
}

// MarshalText implements encoding.TextMarshaler (used by JSON as well).
func (id ID) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (id *ID) UnmarshalText(b []byte) error {
	return id.Scan(string(b))
}
