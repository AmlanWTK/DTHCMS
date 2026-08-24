# `api/` — the OpenAPI contract

`openapi.yaml` is the **contract of record** between the backend and every client. Three
surfaces consume this API; an undocumented endpoint is an endpoint no client can be
generated for.

```bash
pnpm run spec:lint      # redocly lint — the document itself
pnpm run api:generate   # regenerate packages/api-client/src/schema.ts
pnpm run spec:docs      # build api/docs.html, self-contained, openable from disk
```

The reasoning behind the conventions — errors, pagination, idempotency, versioning — is in
[`docs/api-conventions.md`](../docs/api-conventions.md). This file is how to work with the
document.

---

## What is in here

| File           |                                                                                   |
| -------------- | --------------------------------------------------------------------------------- |
| `openapi.yaml` | The contract. Hand-written, linted, and the source for everything below.          |
| `docs.html`    | Generated reference page. Gitignored; CI builds it and uploads it as an artifact. |

## Adding an endpoint

1. Add the path to `openapi.yaml` — with an `operationId`, a summary, and an example on
   every response. The linter enforces the first; review should enforce the rest.
2. `pnpm run api:generate` and commit the regenerated client. Never edit
   `packages/api-client/src/schema.ts` by hand — CI regenerates it and fails on a diff.
3. Implement the route. `go test ./internal/platform/httpx` fails until the router and the
   document agree, in both directions.

Reuse the components that are already there rather than inventing a parallel convention:
`Cursor` and `Limit` for lists, `IdempotencyKey` on every write, and the standard responses
for every error an endpoint can produce. They are declared before there are endpoints using
them precisely so that the first one reaches for them.

## What keeps this honest

`backend/internal/platform/httpx/conformance_test.go` walks the router the service actually
builds and compares it to this document — both ways. A served route missing from the
contract fails; a documented route nobody implemented fails too. The second is the one
people forget: it generates a client method that 404s, and the client compiles perfectly
while doing it.

The same test checks the Go error and health structs against the contract's schemas, and
`packages/shared-schemas` checks the Zod parsers against them from the consuming side. Three
languages, one document.

## Today's scope

`/healthz`, `/readyz` and `/version` — the routes the service actually serves. Clinical
routes arrive with the modules that own them, from CP16 onward.

Authentication is documented (`sessionCookie`) and enforced by nothing: the `/v1` middleware
chain is wired, but every link is a pass-through until its checkpoint — authentication at
CP16, device verification at CP18, authorisation at CP20, rate limiting at CP49.
