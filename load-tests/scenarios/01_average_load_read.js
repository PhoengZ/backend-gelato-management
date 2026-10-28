// load-tests/scenarios/01_average_load_read.js
// Type 1: Average Load Test (High-Throughput Read Path)
// Targets: GET /api/v1/flavors and GET /api/v1/inventory/availability through API Gateway (:8080)

import http from 'k6/http';
import { check, sleep } from 'k6';
import { CONFIG, averageLoadOptions } from '../config/options.js';
import { createSummaryHandler } from '../helpers/summary_reporter.js';

export const options = averageLoadOptions;

export const handleSummary = createSummaryHandler('summary-average-load.json');

export default function () {
  const params = {
    headers: {
      'Accept': 'application/json',
    },
    tags: { scenario: 'average_load_read' },
  };

  // 1. Fetch public flavors catalog
  const flavorsRes = http.get(`${CONFIG.GATEWAY_URL}/api/v1/flavors`, params);
  check(flavorsRes, {
    'flavors: status is 200': (r) => r.status === 200,
    'flavors: returns JSON body': (r) => {
      try {
        const body = JSON.parse(r.body);
        return body && Array.isArray(body.items);
      } catch (_) {
        return false;
      }
    },
  });

  // 2. Fetch inventory availability
  const availRes = http.get(`${CONFIG.GATEWAY_URL}/api/v1/inventory/availability`, params);
  check(availRes, {
    'availability: status is 200 or 404': (r) => r.status === 200 || r.status === 404,
  });

  // Brief think-time between user actions
  sleep(0.1);
}
