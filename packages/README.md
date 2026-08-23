# `packages/` — shared workspace packages

Code that must behave identically across surfaces lives here rather than being
duplicated per application.

| Package          | Purpose                                                                 | Built at |
| ---------------- | ----------------------------------------------------------------------- | -------- |
| `design-tokens`  | One token source → web CSS variables, NativeWind theme, print styles    | CP09     |
| `ui`             | Web primitives built on those tokens: bilingual, accessible, unclinical | CP09     |
| `api-client`     | TypeScript client generated from `api/openapi.yaml`                     | CP12     |
| `shared-schemas` | Zod schemas used by both web and mobile, so validation cannot drift     | CP12     |
| `clinical-calc`  | BMI, BMR, eGFR, growth percentiles — with Go ↔ TypeScript parity tests  | CP43     |

`clinical-calc` deserves particular care: derived clinical values are computed on the
client for instant feedback and on the server for authority. Two implementations that
disagree would be a patient-safety defect, so both are tested against a shared fixture
file of input/expected pairs in CI.

`ui` is web-only by necessity rather than by choice: React DOM and React Native do not
share primitives, so the station apps get the same _tokens_ through NativeWind and their
own components (CP11). What must not diverge is the token layer, and it cannot — there is
one of it.
