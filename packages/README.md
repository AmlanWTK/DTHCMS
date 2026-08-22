# `packages/` — shared workspace packages

Code that must behave identically across surfaces lives here rather than being
duplicated per application.

| Package          | Purpose                                                                | Built at |
| ---------------- | ---------------------------------------------------------------------- | -------- |
| `design-tokens`  | One token source → web CSS variables, NativeWind theme, print styles   | CP09     |
| `api-client`     | TypeScript client generated from `api/openapi.yaml`                    | CP12     |
| `shared-schemas` | Zod schemas used by both web and mobile, so validation cannot drift    | CP12     |
| `clinical-calc`  | BMI, BMR, eGFR, growth percentiles — with Go ↔ TypeScript parity tests | CP43     |

`clinical-calc` deserves particular care: derived clinical values are computed on the
client for instant feedback and on the server for authority. Two implementations that
disagree would be a patient-safety defect, so both are tested against a shared fixture
file of input/expected pairs in CI.

Empty until CP09.
