package serviceaccount

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// countingAuth records how many times the backing store is hit and can be
// flipped to revoke a token.
type countingAuth struct {
	calls   atomic.Int64
	revoked atomic.Bool
}

func (c *countingAuth) Authenticate(_ string) (Account, error) {
	c.calls.Add(1)
	if c.revoked.Load() {
		return Account{}, fmt.Errorf("revoked")
	}
	return Account{ID: "acct", Name: "svc"}, nil
}

func TestCachingAuthenticator(t *testing.T) {
	Convey("Given a caching authenticator with a fixed clock", t, func() {
		inner := &countingAuth{}
		now := time.Unix(1_700_000_000, 0)
		c := &cachingAuthenticator{
			inner: inner,
			ttl:   30 * time.Second,
			now:   func() time.Time { return now },
			cache: map[string]cacheEntry{},
		}

		Convey("When the same token is authenticated twice within the TTL", func() {
			a1, err1 := c.Authenticate("ft_acct_secret")
			a2, err2 := c.Authenticate("ft_acct_secret")

			Convey("Then the backing store is hit only once", func() {
				So(err1, ShouldBeNil)
				So(err2, ShouldBeNil)
				So(a1.ID, ShouldEqual, "acct")
				So(a2.ID, ShouldEqual, "acct")
				So(inner.calls.Load(), ShouldEqual, 1)
			})
		})

		Convey("When the token is revoked but still inside the cached window", func() {
			_, _ = c.Authenticate("ft_acct_secret")
			inner.revoked.Store(true)
			_, err := c.Authenticate("ft_acct_secret")

			Convey("Then the cached success is still served", func() {
				So(err, ShouldBeNil)
			})

			Convey("But once the TTL elapses the revocation takes effect", func() {
				now = now.Add(31 * time.Second)
				_, err := c.Authenticate("ft_acct_secret")
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When authentication fails", func() {
			inner.revoked.Store(true)
			_, err1 := c.Authenticate("ft_acct_secret")
			inner.revoked.Store(false)
			_, err2 := c.Authenticate("ft_acct_secret")

			Convey("Then the failure is not cached and a later fix works immediately", func() {
				So(err1, ShouldNotBeNil)
				So(err2, ShouldBeNil)
				So(inner.calls.Load(), ShouldEqual, 2)
			})
		})
	})

	Convey("Given a non-positive TTL", t, func() {
		inner := &countingAuth{}
		Convey("When wrapping the authenticator", func() {
			wrapped := NewCachingAuthenticator(inner, 0)
			Convey("Then caching is disabled and the inner store is returned unchanged", func() {
				So(wrapped, ShouldEqual, inner)
			})
		})
	})
}
