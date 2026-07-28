package serviceaccount

import (
	"context"
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
	// byAccount indexes cached tokens by the account they resolve to, so a
	// rotation or revocation can evict exactly that account's entries. The
	// cache is keyed by token, which is the only thing a request presents, so
	// without this index a rotated secret kept authenticating for the whole
	// TTL — while RotateSecret's documentation promised it stopped
	// immediately.
	byAccount map[string]map[string]struct{}
	// byTenant indexes cached tokens by the tenant they resolve to, so a role
	// edit or a tenant deactivation can evict every affected entry. Neither is
	// expressible per account: a role change alters what many accounts may do
	// at once, and the cache is keyed by token.
	byTenant map[string]map[string]struct{}
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
		inner:     auth,
		ttl:       ttl,
		now:       time.Now,
		cache:     map[string]cacheEntry{},
		byAccount: map[string]map[string]struct{}{},
		byTenant:  map[string]map[string]struct{}{},
	}
}

// Authenticate satisfies Authenticator; it resolves with a background context.
// The middleware calls AuthenticateCtx instead so the request context flows to
// the backing store.
func (c *cachingAuthenticator) Authenticate(token string) (Account, error) {
	return c.AuthenticateCtx(context.Background(), token)
}

// AuthenticateCtx resolves a token, threading ctx to the wrapped store when it
// is context-aware. A cache hit returns without touching the store or ctx.
func (c *cachingAuthenticator) AuthenticateCtx(ctx context.Context, token string) (Account, error) {
	now := c.now()

	c.mu.RLock()
	entry, ok := c.cache[token]
	c.mu.RUnlock()
	if ok && now.Before(entry.expires) {
		return entry.account, nil
	}

	var account Account
	var err error
	if inner, ok := c.inner.(AuthenticatorCtx); ok {
		account, err = inner.AuthenticateCtx(ctx, token)
	} else {
		account, err = c.inner.Authenticate(token)
	}
	if err != nil {
		return Account{}, err
	}

	c.mu.Lock()
	// Opportunistically drop expired entries so the map can't grow
	// unbounded from rotated tokens.
	if len(c.cache) > 1024 {
		for k, e := range c.cache {
			if !now.Before(e.expires) {
				c.forget(k)
			}
		}
	}
	c.cache[token] = cacheEntry{account: account, expires: now.Add(c.ttl)}
	if c.byAccount == nil {
		c.byAccount = map[string]map[string]struct{}{}
	}
	if c.byAccount[account.ID] == nil {
		c.byAccount[account.ID] = map[string]struct{}{}
	}
	c.byAccount[account.ID][token] = struct{}{}
	if c.byTenant == nil {
		c.byTenant = map[string]map[string]struct{}{}
	}
	if c.byTenant[account.TenantID] == nil {
		c.byTenant[account.TenantID] = map[string]struct{}{}
	}
	c.byTenant[account.TenantID][token] = struct{}{}
	c.mu.Unlock()
	return account, nil
}

// Invalidate drops every cached token for one account, so a rotated or revoked
// credential stops working at once rather than at the end of the TTL.
//
// It satisfies the Invalidator interface, which the admin interactor calls
// after RotateSecret and Revoke. Invalidating an account that has nothing
// cached is a no-op.
func (c *cachingAuthenticator) Invalidate(accountID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for token := range c.byAccount[accountID] {
		c.forget(token)
	}
	delete(c.byAccount, accountID)
}

// InvalidateTenant drops every cached token for one tenant, so a role edit or
// a tenant deactivation takes effect at once.
//
// Roles are the product's bulk-permission mechanism, so the change that most
// needs to apply immediately — removing a grant from a role during an
// incident — was the one silently deferred by up to the TTL on every replica,
// with the API having already confirmed it.
func (c *cachingAuthenticator) InvalidateTenant(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for token := range c.byTenant[tenantID] {
		c.forget(token)
	}
	delete(c.byTenant, tenantID)
}

// forget removes one token from both maps. The caller holds the lock.
func (c *cachingAuthenticator) forget(token string) {
	entry, ok := c.cache[token]
	if !ok {
		return
	}
	delete(c.cache, token)
	if tokens := c.byAccount[entry.account.ID]; tokens != nil {
		delete(tokens, token)
		if len(tokens) == 0 {
			delete(c.byAccount, entry.account.ID)
		}
	}
	if tokens := c.byTenant[entry.account.TenantID]; tokens != nil {
		delete(tokens, token)
		if len(tokens) == 0 {
			delete(c.byTenant, entry.account.TenantID)
		}
	}
}
