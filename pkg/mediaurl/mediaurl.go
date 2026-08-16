// Package mediaurl mints and verifies signed, expiring links to a stored
// media object.
//
// Media bytes live behind the same authentication as the rest of the API, and
// the token carries the tenant. A public surface — a storefront, a catalogue
// page, an email — therefore could not link to an image at all: it had to
// proxy every request through a service holding a tenant credential, which
// carries the whole file through a process that has no other reason to touch
// it and defeats any CDN in front of it.
//
// A redemption is served with `Cache-Control: public`, so a CDN in front of
// the route can hold it: the signature is part of the URL, so it is the cache
// key. The freshness window is short and bounded by the token's remaining life
// — an erasure must be able to take effect while a link is still valid, and
// the service cannot purge a cache it does not own.
//
// A signed link is issued by an authenticated caller and redeemed by anyone
// holding it. That is the point, so the rules are narrow:
//
//   - The signature covers the tenant, the object key AND the expiry
//     together. A signature over the key alone could be replayed against
//     another object, and one that left the expiry out could be replayed
//     forever.
//   - The tenant is read back OUT of the verified token at redemption. It is
//     never taken from a query parameter, so a holder cannot point a valid
//     signature at another tenant's object.
//   - The key is separate from the webhook signing key. One leaked secret
//     must not forge both an event and a file link.
package mediaurl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TokenVersion prefixes every token, so the format can change without a
// verifier mistaking an old token for a corrupt new one.
const TokenVersion = "v1"

// DefaultTTL is how long a link lasts when the caller asks for no particular
// lifetime. Short by default: a link is a bearer credential for one file.
const DefaultTTL = 15 * time.Minute

// MaxTTL caps what a caller may ask for. A caller that wants a link to
// outlive this should mint a new one.
const MaxTTL = 24 * time.Hour

// MinSecretLength is the shortest signing key Signer accepts. A short key is
// brute-forcible offline, and every link ever issued is forgeable once it
// falls.
const MinSecretLength = 32

var (
	// ErrMalformed is returned for a token that is not in the expected shape.
	ErrMalformed = errors.New("flexitype: malformed media token")
	// ErrSignature is returned when the signature does not match. It is
	// deliberately indistinguishable from ErrMalformed to a caller that only
	// reports "not found".
	ErrSignature = errors.New("flexitype: media token signature does not match")
	// ErrExpired is returned for a correctly signed token past its expiry.
	ErrExpired = errors.New("flexitype: media token has expired")
)

// Signer mints and verifies media tokens.
type Signer struct {
	secret []byte
}

// NewSigner builds a signer over a deployment secret.
//
// The secret must be at least MinSecretLength characters. It must not be the
// webhook signing key: a leak of one would otherwise forge the other.
func NewSigner(secret string) (*Signer, error) {
	if len(secret) < MinSecretLength {
		return nil, fmt.Errorf(
			"flexitype: the media URL signing key must be at least %d characters", MinSecretLength)
	}
	return &Signer{secret: []byte(secret)}, nil
}

// Claims are what a verified token asserts.
type Claims struct {
	// Tenant owns the object. It is read out of the SIGNED token, never from
	// the request.
	Tenant string
	// ObjectKey addresses the stored object.
	ObjectKey string
	// ExpiresAt is when the link stops working.
	ExpiresAt time.Time
}

// Sign mints a token for one object, valid for ttl from now.
//
// A non-positive ttl takes DefaultTTL; anything above MaxTTL is capped rather
// than refused, so a caller asking for a year gets a day instead of an error
// it has no way to act on.
func (s *Signer) Sign(tenant, objectKey string, ttl time.Duration, now time.Time) (string, time.Time, error) {
	if tenant == "" || objectKey == "" {
		return "", time.Time{}, fmt.Errorf("flexitype: a media token needs a tenant and an object key")
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if ttl > MaxTTL {
		ttl = MaxTTL
	}
	expires := now.Add(ttl).UTC().Truncate(time.Second)

	payload := encodePayload(tenant, objectKey, expires)
	return payload + "." + s.sign(payload), expires, nil
}

// Verify checks a token and returns what it asserts.
//
// It returns ErrExpired only for a token whose signature is CORRECT: an
// expired-looking token with a bad signature reports the signature failure, so
// the expiry of a forged token is never confirmed.
func (s *Signer) Verify(token string, now time.Time) (Claims, error) {
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || payload == "" || signature == "" {
		return Claims{}, ErrMalformed
	}
	expected := s.sign(payload)
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return Claims{}, ErrSignature
	}
	claims, err := decodePayload(payload)
	if err != nil {
		return Claims{}, err
	}
	if !now.Before(claims.ExpiresAt) {
		return Claims{}, ErrExpired
	}
	return claims, nil
}

func (s *Signer) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// claimSet is the signed part of a token.
//
// It is JSON rather than a delimited string because an object key is
// caller-influenced: a key holding whatever delimiter was chosen would either
// have to be refused — which would make the feature unusable for the keys
// flexitype actually mints, since they carry a file extension — or would let
// two different (tenant, key) pairs encode to the same bytes.
type claimSet struct {
	Version   string `json:"v"`
	ExpiresAt int64  `json:"exp"`
	Tenant    string `json:"t"`
	ObjectKey string `json:"k"`
}

// encodePayload renders the signed part: version, expiry, tenant and key.
func encodePayload(tenant, objectKey string, expires time.Time) string {
	raw, err := json.Marshal(claimSet{
		Version:   TokenVersion,
		ExpiresAt: expires.Unix(),
		Tenant:    tenant,
		ObjectKey: objectKey,
	})
	if err != nil {
		// Every field is a string or an int64, so this cannot fail.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePayload(payload string) (Claims, error) {
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var claims claimSet
	if err := json.Unmarshal(raw, &claims); err != nil {
		return Claims{}, ErrMalformed
	}
	if claims.Version != TokenVersion || claims.Tenant == "" || claims.ObjectKey == "" {
		return Claims{}, ErrMalformed
	}
	return Claims{
		Tenant:    claims.Tenant,
		ObjectKey: claims.ObjectKey,
		ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
	}, nil
}
