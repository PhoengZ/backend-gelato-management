// load-tests/scenarios/02_spike_stress_order.js
// Type 2: Spike / Stress Test (Transactional Preorder Contention & Concurrency)
// Targets: POST /api/v1/orders through API Gateway (:8080)

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate } from 'k6/metrics';
import { CONFIG, spikeStressOptions } from '../config/options.js';
import { mintCustomerToken } from '../helpers/auth.js';
import { generateUUID, buildOrderPayload } from '../helpers/payload.js';
import { createSummaryHandler } from '../helpers/summary_reporter.js';

export const options = spikeStressOptions;

export const handleSummary = createSummaryHandler('summary-spike-stress.json');

// Custom metrics to track business outcomes
const ordersCreated = new Counter('orders_created');
const stockConflicts = new Counter('stock_conflicts');
const rateLimited = new Counter('rate_limited');
const serverErrors = new Counter('server_errors');
const orderSuccessRate = new Rate('order_success_rate');

// Discover a sample flavor ID in init / setup
export function setup() {
  try {
    const res = http.get(`${CONFIG.GATEWAY_URL}/api/v1/flavors`);
    if (res.status === 200) {
      const data = JSON.parse(res.body);
      if (data.items && data.items.length > 0) {
        return { flavorId: data.items[0].id };
      }
    }
  } catch (_) {}
  return { flavorId: '4e912a37-0efd-4327-bab8-843244c25ce9' };
}

export default function (data) {
  // Mint unique customer token with a valid UUID subject
  const token = mintCustomerToken();
  const idempotencyKey = generateUUID();
  const payload = JSON.stringify(buildOrderPayload(data.flavorId, 1));

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Accept': 'application/json',
      'Authorization': `Bearer ${token}`,
      'Idempotency-Key': idempotencyKey,
    },
    tags: { scenario: 'spike_stress_order' },
  };

  const res = http.post(`${CONFIG.GATEWAY_URL}/api/v1/orders`, payload, params);

  // Analyze response outcomes
  if (res.status === 201) {
    ordersCreated.add(1);
    orderSuccessRate.add(1);
  } else if (res.status === 409) {
    stockConflicts.add(1);
    orderSuccessRate.add(0);
  } else if (res.status === 429) {
    rateLimited.add(1);
    orderSuccessRate.add(0);
  } else if (res.status >= 500) {
    serverErrors.add(1);
    orderSuccessRate.add(0);
  } else {
    orderSuccessRate.add(0);
  }

  check(res, {
    'no 5xx server crash': (r) => r.status < 500,
    'valid business response (201, 409, or 429)': (r) => [201, 409, 429].includes(r.status),
  });

  // Short pause under spike conditions
  sleep(0.05);
}
