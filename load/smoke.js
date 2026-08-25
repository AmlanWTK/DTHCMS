import http from 'k6/http';
import { check } from 'k6';

/*
 * The smallest possible k6 script: does the API answer, and how fast.
 *
 * Scaffolding for CP93, not a load test. It hits the two operational endpoints because
 * they are the only routes that exist at CP13 and because they need no authentication —
 * which means this file stays runnable as a connectivity check long after CP93 has
 * written the real scenarios beside it.
 */

export const options = {
  vus: 1,
  duration: '10s',
  thresholds: {
    // Deliberately generous. A threshold tuned against an empty application would have to
    // be retuned the moment it does anything, and a threshold nobody trusts gets deleted.
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
  },
};

const BASE = __ENV.DTHCMS_API_URL || 'http://localhost:8080';

export default function () {
  const live = http.get(`${BASE}/healthz`);
  check(live, {
    'healthz answers 200': (r) => r.status === 200,
    'healthz reports ok': (r) => r.json('status') === 'ok',
  });

  // /readyz touches every dependency, so it is the one that will actually show strain.
  const ready = http.get(`${BASE}/readyz`);
  check(ready, { 'readyz answers': (r) => r.status === 200 || r.status === 503 });
}
