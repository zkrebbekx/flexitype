# The shopper storefront

React over the storefront's projected catalog. One page, every merchant.

```bash
npm install
npm run dev            # http://localhost:5175
```

In the compose stack it is served by nginx on <http://localhost:8082>.

## What it demonstrates

**One search across many tenants.** flexitype takes the tenant from the token,
so "every merchant's active products, ranked by relevance" is not expressible
against it. This page therefore reads the storefront's OWN catalog, which its
webhook projector maintains. That is the whole reason the projection exists.

**Heterogeneous schemas, one product page.** Everything a merchant added to its
own subtype arrives under `attributes`, keyed by internal name, and is rendered
without this application knowing a single one of those names.

**A withdrawn product is a 404.** A draft or an archived product is not hidden
field by field; the row is refused, so its exact URL answers 404 and its image
is unreachable.

**A price is never parsed.** It is a decimal string from the projection and it
is rendered as text. A float parse turns `89.50` into `89.5`, and a long price
into a wrong one.

## Credentials

None. The shopper API is public. The product image is proxied by the
storefront, which holds the merchant's token server-side, so a shopper never
receives one.

## Tests

```bash
npm test
```

They cover products from two tenants in one grid, filters reaching the API
rather than being applied in the browser, a merchant's own fields rendering
unknown, a withdrawn product, and a basket that keeps two merchants' products
apart when their ids collide.
