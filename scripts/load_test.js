import http from 'k6/http';
import { check, sleep } from 'k6';

// k6 Load Test Configuration: Flash-Sale Contention Barrage
export const options = {
  stages: [
    { duration: '5s', target: 20 },  // Ramp-up to 20 VUs
    { duration: '15s', target: 100 }, // Peak flash-sale spike: 100 VUs
    { duration: '5s', target: 0 },   // Ramp-down
  ],
  thresholds: {
    http_req_duration: ['p(95)<50'], // 95% of requests must complete under 50ms
    http_req_failed: ['rate<0.01'],  // Less than 1% network/unexpected failures
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const TARGET_SKU = __ENV.TARGET_SKU || 'VIP-CONCERT-TICKET-2026';

export default function () {
  const url = `${BASE_URL}/api/v1/reservations/hold`;

  // Random unique idempotency key per iteration
  const idempotencyKey = `k6-hold-${__VU}-${__ITER}-${Date.now()}`;
  const customerId = `customer-vu-${__VU}`;

  const payload = JSON.stringify({
    sku: TARGET_SKU,
    customer_id: customerId,
    idempotency_key: idempotencyKey,
    quantity: 1,
    ttl_seconds: 120,
  });

  const params = {
    headers: {
      'Content-Type': 'application/json',
    },
  };

  const res = http.post(url, payload, params);

  // Both 201 (Successfully Reserved) and 409 (Out of Stock) are expected and valid responses
  check(res, {
    'status is 201 (Reserved) or 409 (Out of Stock)': (r) => r.status === 201 || r.status === 409,
    'response has valid JSON body': (r) => {
      try {
        JSON.parse(r.body);
        return true;
      } catch (e) {
        return false;
      }
    },
  });

  sleep(0.05); // 50ms pause between rapid clicks
}
