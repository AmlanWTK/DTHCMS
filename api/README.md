# `api/` — OpenAPI contract

The OpenAPI 3.1 specification lives here and is the **contract of record** between the
backend and every client. The TypeScript client in `packages/api-client` is generated
from it, and CI fails if an implemented route is missing from the spec.

**Built at CP12.** Empty until then, deliberately.
