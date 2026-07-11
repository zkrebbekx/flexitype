# flexitype Admin Console — Design

Design rationale for the administrator UI. The console is where schema
owners model soft types and where operators observe what those types hold
in production. It lives at `/` in the standalone service (the API stays at
`/api/v1`) and is developed in `web/`.

## Personas

**Schema administrator** ("Priya, platform engineer"). Owns the data model.
Creates type definitions, attributes, constraints and dependencies. Cares
about: not breaking existing data, understanding the blast radius of an
edit (definition versions, value counts), and expressing rules without
reading API docs.

**Operator** ("Marco, support engineer"). Investigates entities. Needs to
answer "what values does order-1234 hold, are they valid, who changed them
and when?" in under a minute. Rarely edits schema; occasionally fixes a
value. Cares about: fast search, a truthful audit trail, and the editor
stopping him from writing an invalid value.

## Core journeys

1. **Model a type** — create type → add attributes one by one (data type
   drives the constraint form) → sanity-check with "try a value" before
   anything ships → wire dependencies for cascading picklists.
2. **Evolve safely** — open an attribute → see its current version and how
   many live values pin to it → edit → the version bump is explicit in the
   confirmation, not a surprise.
3. **Observe an entity** — pick a type → entity browser (distinct entities
   with value counts, most recently touched first) → entity inspector shows
   every value with its definition, version pinning and staleness → inline
   edit is dependency-aware: the editor asks the server for the effective
   schema and narrows choices *before* submit.
4. **Audit a change** — activity stream, filterable by entity/actor →
   each row expands to a before/after diff of the JSON descriptors the
   backend already records.

## Information architecture

```
Sidebar (fixed, icon+label)
├── Types            → /types                (primary landing)
│    └── Type detail → /types/:id            tabs: Attributes | Dependencies | Entities
├── Entities         → /entities             cross-type browser (pick type → list)
│    └── Inspector   → /entities/:typeId/:entityId
├── Activity         → /activity             global stream w/ filters
└── (footer) health chip + version
```

Depth is capped at three levels; every deep page carries breadcrumbs. All
list pages share one pagination idiom (the API's cursor + limit) and one
empty-state pattern (explain the concept + primary action).

## Interaction decisions

- **Drawers, not page jumps, for editing.** Attribute and dependency
  editors open as right-side drawers over the list they came from —
  context (the sibling attributes you're naming against) stays visible.
- **The data type drives the form.** Choosing `enum` reveals the members
  editor and hides length bounds; choosing `integer` reveals min/max value;
  pattern only appears for textual types. Illegal combinations are
  unrepresentable in the UI, mirroring the domain rules.
- **"Try a value" everywhere a rule is authored.** The attribute drawer
  embeds a live tester that calls the real validation path, so admins see
  the exact error an API consumer would get.
- **Dependency-aware value editing.** The entity inspector fetches
  `effective-schema` when an editor opens: allowed values render as the
  only choices, `required` renders as a badge, and a restricted-to-empty
  set renders as an explicit "no value currently allowed" state (the
  conflicting dependencies are listed).
- **Domain error codes are first-class.** `VALIDATION`, `CONFLICT`,
  `DEPENDENCY_VIOLATION`, `ARCHIVED`, `NOT_FOUND` map to distinct, plain
  language inline messages — never a toast with a raw 422.
- **Archive is soft and visible.** Archived items stay listable behind a
  filter toggle, rendered dimmed with a Restore affordance. No destructive
  irreversibility anywhere in the console.
- **Audit diff.** Before/after JSON rendered as a two-column diff with
  changed keys highlighted; identical keys collapsed by default.

## Design system

Neutral, dense-but-calm admin aesthetic. No component library — a small
owned kit keeps the bundle lean and the look coherent.

- **Type**: system font stack; 13px base in tables, 14px in forms; a
  single display size for page titles. Tabular numerals for counts.
- **Color**: neutral slate ramp for chrome; one indigo accent for primary
  actions and active navigation; semantic green/amber/red reserved for
  status only. Light and dark themes from day one via CSS variables +
  `prefers-color-scheme`, with a manual toggle persisted to localStorage.
- **Surfaces**: white/near-black cards on a faint canvas, 1px borders,
  6px radii, shadows only on overlays (drawer, modal, menus).
- **Data types get glyph + tint chips** (`Aa` string, `#` integer, `1.0`
  float/decimal, `◧` bool, `⏱` temporal, `⋯` enum, `{}` json, `@` email,
  `⌘` url) so scanning an attribute table is pre-attentive.
- **Motion**: 150ms ease-out for drawers/menus; skeleton rows for loading;
  no spinners longer than 300ms without a skeleton.
- **Accessibility**: every interactive element keyboard-reachable, focus
  rings visible, drawers trap focus and restore it, tables are real
  `<table>` semantics, color never the sole signal (chips carry glyphs).

## Component kit

Button (primary/secondary/ghost/danger), Input, Select, Toggle, Badge,
Chip (data-type), Table (sticky header, skeleton rows), Drawer, Modal
(confirm), Tabs, Toast (errors that aren't inline), EmptyState, JsonView
(collapsible), DiffView, Pagination (cursor-based), PageHeader
(breadcrumb + actions).

## Stack (principal frontend engineer decision)

**Vue 3** (`<script setup>` + TypeScript) — chosen deliberately per the
project's direction to evaluate Vue; its SFC model suits a form-heavy
console and Composition API composables map cleanly onto the API client.

- **Vite 7** — build/dev; dev server proxies `/api` to the Go service so
  no CORS pathway exists to misconfigure.
- **vue-router 4** — flat route table mirroring the IA above.
- **@tanstack/vue-query 5** — server state: caching, invalidation after
  mutations (mirrors the dataloader thinking server-side). No Pinia: the
  only client state is theme + toasts, which two tiny composables cover.
- **Tailwind CSS 4** — utility styling against CSS-variable tokens; the
  kit owns all visual decisions, Tailwind just applies them.
- **lucide-vue-next** — icons.
- **Vitest + @vue/test-utils + happy-dom** — component and unit tests;
  the API client is tested against a stubbed `fetch`.
- **No component framework** (Vuetify/PrimeVue rejected: bundle weight,
  look-and-feel lock-in, and the kit above is ~15 small components).

Production serving: `vite build` output is embedded into the Go binary
(`web/embed.go` + `//go:embed dist`) with an SPA fallback handler; one
artifact ships the whole product. The embed is build-tag-free — a stub
`dist/index.html` is committed so `go build` works without Node.
