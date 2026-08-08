# The merchant console

React on the TypeScript SDK (`@flexitype/client`). A merchant models its own
schema here and edits its products through a form the SCHEMA draws.

```bash
npm install
PLATFORM_API_TOKEN=platform-console-demo-token npm run dev   # http://localhost:5174
```

In the compose stack it is served by nginx on <http://localhost:8081>.

## What it demonstrates

**A form nobody wrote.** `ProductEditorPage` reads the type's EFFECTIVE
attributes with the SDK's `useFormDescriptor`, and `DynamicForm` renders them.
No product field is named anywhere in this application. A merchant that adds
`voltage` to its own subtype gets a `voltage` input with no change here.

**Coercion before the request.** The SDK's `toWire` turns what an HTML form
yields into the wire form each data type takes, and refuses a value that cannot
be that type. The failure carries the same code — `VALIDATION` — the service
would have answered with, so one error path covers both.

**A decimal stays text.** A price is rendered in a text input with a decimal
keypad, never `type="number"`. The browser's number parser drops the trailing
zero of `89.50`, and past fifteen digits it drops real ones.

## How it reaches flexitype

The browser holds no credential at all. Two paths, for two reasons:

| Path | Goes to | Why |
| --- | --- | --- |
| Reads | The platform's `/flexitype/api/v1/…` passthrough | Real flexitype paths, so the SDK's client, hooks and soft-typing helpers work unchanged. The platform attaches the merchant's service-account token. |
| Writes | The platform's own endpoints | They batch a whole product into ONE atomic call. Writing value by value would let the storefront project a half-written product. |

The passthrough is read-only, and says so: a write through it answers 405. See
[`../platform/passthrough.go`](../platform/passthrough.go).

nginx adds the console credential to every proxied request
([`nginx.conf.template`](nginx.conf.template)), and the Vite dev server does
the same thing with `PLATFORM_API_TOKEN`, so `npm run dev` and the built
console behave identically.

A real deployment authenticates a merchant USER here and derives the merchant
id from the session. This example uses one shared console credential to stay
runnable.

## A gotcha worth knowing

The SDK is a LINKED dependency (`file:../../../client-ts`), so it brings its
own `node_modules`. React and TanStack Query are its peer dependencies. Without
`resolve.dedupe` in [`vite.config.ts`](vite.config.ts), the production build
resolves the SDK's copy of TanStack Query while the application uses its own:
two copies means two React contexts, and every SDK hook throws
`No QueryClient set` although the application has a provider. The development
server dedupes by itself, so the failure appears only in the built console.

## Tests

```bash
npm test
```

They cover the form the schema draws — including a field the console never
names — the wire form each data type produces, a value that cannot be its type,
a localized write, and that no request carries a credential.
