# `packages/` — shared workspace packages

Code that must behave identically across surfaces lives here rather than being
duplicated per application.

| Package          | Purpose                                                                                | Built at |
| ---------------- | -------------------------------------------------------------------------------------- | -------- |
| `design-tokens`  | One token source → web CSS variables, NativeWind theme, print styles                   | CP09     |
| `ui`             | Web primitives built on those tokens: bilingual, accessible, unclinical                | CP09     |
| `api-client`     | TypeScript client generated from `api/openapi.yaml`, plus the hand-written error layer | CP12     |
| `shared-schemas` | Zod schemas used by both web and mobile, so validation cannot drift                    | CP12     |
| `clinical-calc`  | BMI, BMR, eGFR, growth percentiles — with Go ↔ TypeScript parity tests                 | CP43     |

`api-client` and `shared-schemas` split one job deliberately. The first holds the
_generated types_ — `src/schema.ts`, produced from the contract and never hand-edited — and
the small runtime around them: which failure becomes an `ApiError` and which a
`NetworkError`, where the correlation ID comes from, and what is worth retrying. The second
holds the _runtime parsers_, because types are erased at runtime and a backend that renames
a field otherwise ships past `tsc` and surfaces as `undefined` in a table cell three screens
from the cause. Both are checked against `api/openapi.yaml` by tests that fail on drift.

`clinical-calc` deserves particular care: derived clinical values are computed on the
client for instant feedback and on the server for authority. Two implementations that
disagree would be a patient-safety defect, so both are tested against a shared fixture
file of input/expected pairs in CI.

`ui` is web-only by necessity rather than by choice: React DOM and React Native do not
share primitives, so the station apps get the same _tokens_ through NativeWind and their
own components (CP11). What must not diverge is the token layer, and it cannot — there is
one of it.
