// The k6 load test for every profile in profiles.json. Run it through make
// (make perf PROFILE=load), which starts the stack and runs k6 in Docker on
// the stack's network; docs/performance.md explains the profiles and how to
// read the results.
//
// A profile is a list of phases run one after another, each at a fixed
// arrival rate (requests per second) or ramping to one from "from". The rate
// is held whatever the latency: k6 starts new requests on schedule instead of
// waiting for slow ones, so a slow service can't hide by slowing the test
// down. Every request is tagged with its phase, which is how thresholds in
// profiles.json can apply to one phase only, leaving the warm-up out.
//
// Environment: PROFILE (default smoke), API_URL (default http://api:8080),
// API_KEY (default the stack's development key, which may write).

import http from 'k6/http';
import { check } from 'k6';

const profiles = JSON.parse(open('./profiles.json'));
const profileName = __ENV.PROFILE || 'smoke';
const profile = profiles[profileName];
if (!profile) {
  throw new Error(`unknown PROFILE "${profileName}"; profiles.json has: ${Object.keys(profiles).join(', ')}`);
}
const baseURL = (__ENV.API_URL || 'http://api:8080').replace(/\/$/, '');

// seconds parses a k6 duration such as "30s", "3m" or "1h".
function seconds(d) {
  const m = /^(\d+)(s|m|h)$/.exec(d);
  if (!m) throw new Error(`bad duration "${d}": use a whole number of s, m or h`);
  return Number(m[1]) * { s: 1, m: 60, h: 3600 }[m[2]];
}

// scenarios turns the profile's phases into k6 scenarios that run back to back.
function scenarios(phases) {
  const out = {};
  let start = 0;
  for (const p of phases) {
    // Enough virtual users that a request taking up to a second at this rate
    // never has to wait for one; k6 adds more, up to maxVUs, if they do.
    const vus = Math.max(10, Math.ceil(p.rate / 10));
    const common = {
      exec: 'traffic',
      startTime: `${start}s`,
      timeUnit: '1s',
      preAllocatedVUs: vus,
      maxVUs: Math.max(vus, p.rate),
      tags: { phase: p.name },
    };
    out[p.name] = p.from === undefined
      ? { ...common, executor: 'constant-arrival-rate', rate: p.rate, duration: p.duration }
      : { ...common, executor: 'ramping-arrival-rate', startRate: p.from, stages: [{ target: p.rate, duration: p.duration }] };
    start += seconds(p.duration);
  }
  return out;
}

export const options = {
  scenarios: scenarios(profile.phases),
  thresholds: profile.thresholds,
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(99)', 'p(99.9)', 'max'],
  // The bodies aren't needed beyond the status check, and reading them costs
  // the load generator CPU that would otherwise make load.
  discardResponseBodies: true,
};

// The traffic mix: mostly reads, as an API like this sees. The IDs are the
// items the WireMock stubs serve, so every request is meant to succeed and
// any failure counts against the error thresholds.
const ids = ['widget', 'gadget', 'gizmo'];
const json = { headers: { 'Content-Type': 'application/json' } };
const auth = `Bearer ${__ENV.API_KEY || 'gvs_local-development-only'}`;
const mix = [
  { weight: 40, route: 'GET /items', call: () => http.get(`${baseURL}/items`, tagged('GET /items')), want: 200 },
  { weight: 40, route: 'GET /items/{id}', call: () => http.get(`${baseURL}/items/${pick(ids)}`, tagged('GET /items/{id}')), want: 200 },
  { weight: 10, route: 'POST /items', call: () => http.post(`${baseURL}/items`, '{"name":"Load test","description":"Created by k6."}', tagged('POST /items', json)), want: 201 },
  { weight: 5, route: 'PUT /items/{id}', call: () => http.put(`${baseURL}/items/${pick(ids)}`, '{"name":"Renamed by k6"}', tagged('PUT /items/{id}', json)), want: 200 },
  { weight: 5, route: 'DELETE /items/{id}', call: () => http.del(`${baseURL}/items/${pick(ids)}`, null, tagged('DELETE /items/{id}')), want: 204 },
];
const totalWeight = mix.reduce((n, m) => n + m.weight, 0);

function pick(list) {
  return list[Math.floor(Math.random() * list.length)];
}

// tagged names a request by its route, not its URL, so the results group
// /items/widget and /items/gizmo together, and adds the API key.
function tagged(route, params = {}) {
  return {
    ...params,
    headers: { ...(params.headers || {}), Authorization: auth },
    tags: { ...(params.tags || {}), name: route },
  };
}

export function traffic() {
  let r = Math.random() * totalWeight;
  const m = mix.find((m) => (r -= m.weight) < 0) || mix[mix.length - 1];
  const res = m.call();
  check(res, { [`${m.route} is ${m.want}`]: (res) => res.status === m.want });
}
