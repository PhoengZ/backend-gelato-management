// load-tests/config/options.js
// Configuration constants, stages, and SLA thresholds for GelatoFlow k6 load tests.

export const CONFIG = {
  GATEWAY_URL: __ENV.GATEWAY_URL || 'http://localhost:8080',
  CATALOG_URL: __ENV.CATALOG_URL || 'http://localhost:3002',
  JWT_SECRET: __ENV.JWT_SECRET || 'development_only_change_this_secret_32_bytes',
  JWT_ISSUER: __ENV.JWT_ISSUER || 'gelatoflow-auth',
  JWT_AUDIENCE: __ENV.JWT_AUDIENCE || 'gelatoflow-api',
};

// Scenario 1: Average Load Test (Baseline Read Path - Catalog & Inventory Availability)
// Simulates normal steady-state peak customer browsing traffic.
export const averageLoadOptions = {
  stages: [
    { duration: '15s', target: 20 },  // Ramp-up to 20 concurrent users
    { duration: '30s', target: 20 },  // Steady-state plateau at 20 VUs
    { duration: '10s', target: 0 },   // Ramp-down back to 0 VUs
  ],
  thresholds: {
    // 95% of requests must complete under 250ms; 99% under 600ms
    http_req_duration: ['p(95)<250', 'p(99)<600'],
    // Less than 1% failure rate allowed
    http_req_failed: ['rate<0.01'],
    // At least 99% of logical checks must pass
    checks: ['rate>0.99'],
  },
};

// Scenario 2: Spike / Stress Test (Transactional Preorder Contention & Slot Capacity)
// Simulates a sudden flash rush of customers placing orders concurrently.
export const spikeStressOptions = {
  stages: [
    { duration: '5s', target: 10 },   // Warm-up with 10 VUs
    { duration: '10s', target: 50 },  // Rapid spike up to 50 concurrent ordering users
    { duration: '20s', target: 50 },  // Heavy contention on stock rows and slot capacity
    { duration: '10s', target: 10 },  // De-escalate spike
    { duration: '5s', target: 0 },    // Ramp-down to 0
  ],
  thresholds: {
    // Under severe database lock contention, p95 should remain under 2500ms
    http_req_duration: ['p(95)<2500'],
    // 5xx server errors must be 0 (409 INSUFFICIENT_STOCK is acceptable business rejection)
    'http_req_failed{status:500}': ['rate==0'],
  },
};
