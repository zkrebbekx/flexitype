package serviceaccount

import (
	"sync"
	"time"
)

// cachingAuthenticator wraps an Authenticator with a short-lived success
// cache so authentication doesn't hit the backing store on every request.
// Only successful resolutions are cached; a revoked or rotated account
// stops working within ttl. Failures are never cached (so a fixed
// credential starts working immediately).
type cachingAuthenticator struct {
	inner Authenticator
	ttl   time.Duration
	now   func() time.Time

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	account Account
	expires time.Time
}

// NewCachingAuthenticator wraps auth with a TTL success cache. A zero or
// negative ttl disables caching.
func NewCachingAuthenticator(auth Authenticator, ttl time.Duration) Authenticator {
	if ttl <= 0 {
		return auth
	}
	return &cachingAuthenticator{
		inner: auth,
		ttl:   ttl,
		now:   time.Now,
		cache: map[string]cacheEntry{},
	}
}

func (c *cachingAuthenticator) Authenticate(token string) (Account, error) {
	now := c.now()

	c.mu.RLock()
	entry, ok := c.cache[token]
	c.mu.RUnlock()
	if ok && now.Before(entry.expires) {
		return entry.account, nil
	}

	account, err := c.inner.Authenticate(token)
	if err != nil {
		return Account{}, err
	}

	c.mu.Lock()
	// Opportunistically drop expired entries so the map can't grow
	// unbounded from rotated tokens.
	if len(c.cache) > 1024 {
		for k, e := range c.cache {
			if !now.Before(e.expires) {
				delete(c.cache, k)
			}
		}
	}
	c.cache[token] = cacheEntry{account: account, expires: now.Add(c.ttl)}
	c.mu.Unlock()
	return account, nil
}
