# `load/` — k6 scaffolding

**Structure only.** Load testing is **CP93**, and running it before there is a synthetic
dataset (CP13) and a real workload (Phase 1) would measure an empty application against
imaginary traffic.

What is here now is the shape: where scripts live, how they are run, and what they will
target — so CP93 writes scenarios rather than plumbing.

```bash
k6 run load/smoke.js                              # once there is something to load
k6 run --vus 50 --duration 5m load/smoke.js
```

## What CP93 will need from CP13

The synthetic generator, at scale. A load test against fifty patients tells you about
cache behaviour and nothing about a clinic; the generator is configurable precisely so
that CP93 can ask for a hundred thousand.
