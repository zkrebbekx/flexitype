package valueobjects

// Scope locates a value along optional presentation dimensions: a locale
// (en_AU, de_DE) and a channel/context (web, print, marketplace). The zero
// Scope is the base/unscoped value. A value's identity within an entity and
// attribute is (locale, channel), so one attribute can hold a different
// value per market and channel.
type Scope struct {
	Locale  string `json:"locale,omitempty"`
	Channel string `json:"channel,omitempty"`
}

// IsZero reports whether the scope is the base (unscoped) value.
func (s Scope) IsZero() bool { return s.Locale == "" && s.Channel == "" }

// Equals reports scope identity.
func (s Scope) Equals(o Scope) bool { return s.Locale == o.Locale && s.Channel == o.Channel }
