# Growth reference data

`build_reference.py` produces two artefacts from the **published** WHO and CDC tables:

- the `INSERT INTO core.growth_lms` block inside
  `backend/migrations/00030_growth_reference.sql`, and
- `packages/clinical-calc/fixtures/growth-reference.json`, the validation set.

It is here so the derivation is reproducible rather than a claim. Nothing in it re-fits,
smooths or interpolates: it selects rows and rewrites them as SQL.

## The sources

| Standard | Ages | What is taken |
| --- | --- | --- |
| WHO Child Growth Standards (2006) | 0–60 months | `L`, `M`, `S` and the printed −3 SD … +3 SD columns for length/height-for-age, weight-for-age and BMI-for-age, both sexes |
| CDC 2000 Growth Charts | from 60 months | `L`, `M`, `S` and the printed P3 … P97 columns for stature-for-age, weight-for-age and BMI-for-age, both sexes |

Two details that are not obvious and are not mistakes:

- **WHO publishes two rows at month 24** — recumbent length up to 24 months, standing height
  from 24 months, about 0.7 cm apart. The table keeps the standing-height row at 24 months,
  because a two-year-old in this clinic is measured standing.
- **CDC's ages fall on half-months** (24.5, 25.5, … 240.5). That is how CDC publishes them,
  not a rounding artefact.

## The validation set is the publishers' own arithmetic

Both sources print, beside their `L/M/S`, the cut-offs those parameters produce. The fixture
holds every one of them — 1,452 age points, roughly twelve thousand values — and CP47's tests
recompute each from the seeded parameters. Nobody in this project wrote those expected
numbers, which is the point: a transcription error fails the build rather than agreeing with
itself.

## Re-running it

The script reads the published files from a directory of extracted source tables and writes
`lms.sql` and `growth-reference.json`. Point `WHO` and `CDC` at that directory, run it, and
paste the SQL block into a new migration — never edit the seeded rows by hand. A parameter
changed in place is a parameter with no provenance.
