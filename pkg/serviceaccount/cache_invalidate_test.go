package serviceaccount

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// staticAuth resolves any token whose secret matches, to a fixed account.
type staticAuth struct {
	accountID string
	secret    string
	calls     int
}

func (s *staticAuth) Authenticate(token string) (Account, error) {
	s.calls++
	id, secret, err := SplitToken(token)
	if err != nil {
		return Account{}, err
	}
	if secret != s.secret {
		return Account{}, errors.New("bad secret")
	}
	return Account{ID: id, TenantID: "acme"}, nil
}

// TestCacheInvalidate covers the eviction that makes rotation and revocation
// immediate.
//
// The cache is keyed by token, which is the only thing a request presents, so
// nothing could evict "every token for this account" — a rotated secret kept
// authenticating for the whole TTL while RotateSecret's documentation promised
// it stopped at once. An operator who records the rotation time and does not
// look for later requests then writes down a wrong incident timeline.
func TestCacheInvalidate(t *testing.T) {
	Convey("Given a cached authentication for an account", t, func() {
		inner := &staticAuth{accountID: "acct", secret: "s1"}
		c := NewCachingAuthenticator(inner, 30*time.Second).(*cachingAuthenticator)
		token := MintToken("acct", "s1")

		_, err := c.AuthenticateCtx(context.Background(), token)
		So(err, ShouldBeNil)
		So(inner.calls, ShouldEqual, 1)

		Convey("When the same token is presented again", func() {
			_, err := c.AuthenticateCtx(context.Background(), token)

			Convey("Then it is served from the cache", func() {
				So(err, ShouldBeNil)
				So(inner.calls, ShouldEqual, 1)
			})
		})

		Convey("When the account is invalidated", func() {
			c.Invalidate("acct")

			Convey("Then the next request consults the store again", func() {
				inner.secret = "s2" // the rotation the invalidation follows
				_, err := c.AuthenticateCtx(context.Background(), token)
				So(err, ShouldNotBeNil)
				So(inner.calls, ShouldEqual, 2)
			})

			Convey("Then the account index is empty, so nothing leaks", func() {
				So(c.byAccount["acct"], ShouldBeEmpty)
				So(c.cache, ShouldBeEmpty)
			})
		})

		Convey("When a different account is invalidated", func() {
			c.Invalidate("someone-else")

			Convey("Then this account's cache entry survives", func() {
				_, err := c.AuthenticateCtx(context.Background(), token)
				So(err, ShouldBeNil)
				So(inner.calls, ShouldEqual, 1)
			})
		})

		Convey("When an account with nothing cached is invalidated", func() {
			Convey("Then it is a no-op rather than a panic", func() {
				So(func() { c.Invalidate("never-seen") }, ShouldNotPanic)
			})
		})

		Convey("When several tokens of one account are cached", func() {
			// A rotation leaves the previous token cached too; invalidating the
			// account must drop every one of them, not just the newest.
			inner.secret = "s2"
			second := MintToken("acct", "s2")
			_, err := c.AuthenticateCtx(context.Background(), second)
			So(err, ShouldBeNil)
			So(len(c.byAccount["acct"]), ShouldEqual, 2)

			c.Invalidate("acct")

			Convey("Then all of them are evicted", func() {
				So(c.cache, ShouldBeEmpty)
				So(c.byAccount, ShouldNotContainKey, "acct")
			})
		})
	})

	Convey("Given a caching authenticator", t, func() {
		c := NewCachingAuthenticator(&staticAuth{secret: "s"}, time.Second)

		Convey("Then it advertises the Invalidator contract the admin API uses", func() {
			_, ok := c.(Invalidator)
			So(ok, ShouldBeTrue)
		})
	})

	Convey("Given caching is disabled", t, func() {
		inner := &staticAuth{secret: "s"}
		c := NewCachingAuthenticator(inner, 0)

		Convey("Then the store is returned unwrapped, with nothing to invalidate", func() {
			So(c, ShouldEqual, inner)
			_, ok := c.(Invalidator)
			So(ok, ShouldBeFalse)
		})
	})
}

// TestCacheEvictsExpiredEntriesWhenLarge covers the sweep that keeps the cache
// from growing without bound.
//
// The cache is keyed by token. A deployment that rotates secrets, or that
// authenticates many accounts, adds one entry per token and never reuses the
// old key, so without the sweep the map grows for the lifetime of the process
// and holds every account it ever saw.
func TestCacheEvictsExpiredEntriesWhenLarge(t *testing.T) {
	Convey("Given a cache holding more than 1024 expired entries", t, func() {
		inner := &staticAuth{secret: "s1"}
		c := NewCachingAuthenticator(inner, time.Minute).(*cachingAuthenticator)

		clock := time.Now()
		c.now = func() time.Time { return clock }
		for i := 0; i < 1100; i++ {
			_, err := c.AuthenticateCtx(context.Background(),
				MintToken("acct"+strconv.Itoa(i), "s1"))
			So(err, ShouldBeNil)
		}
		So(len(c.cache), ShouldEqual, 1100)
		So(len(c.byAccount), ShouldEqual, 1100)

		Convey("When the entries expire and one more token authenticates", func() {
			clock = clock.Add(2 * time.Minute)
			fresh := MintToken("newcomer", "s1")
			_, err := c.AuthenticateCtx(context.Background(), fresh)

			Convey("Then the expired entries are dropped from both maps", func() {
				So(err, ShouldBeNil)
				So(len(c.cache), ShouldEqual, 1)
				So(len(c.byAccount), ShouldEqual, 1)
				_, held := c.cache[fresh]
				So(held, ShouldBeTrue)
				So(c.byAccount["newcomer"], ShouldHaveLength, 1)
			})
		})

		Convey("When the entries are still live and one more token authenticates", func() {
			_, err := c.AuthenticateCtx(context.Background(), MintToken("newcomer", "s1"))

			Convey("Then nothing is dropped: the sweep only removes expired entries", func() {
				So(err, ShouldBeNil)
				So(len(c.cache), ShouldEqual, 1101)
			})
		})
	})
}
