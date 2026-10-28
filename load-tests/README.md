# GelatoFlow k6 Load Testing Suite

Comprehensive performance, load, and concurrency stress testing for the GelatoFlow backend microservices using **k6**.

---

## 📁 Directory Structure

```text
load-tests/
├── config/
│   └── options.js                  # Shared URLs, secret keys, stages, and SLA thresholds
├── helpers/
│   ├── auth.js                     # In-script HS256 JWT token minting for multi-VU simulation
│   ├── payload.js                  # RFC 3339 15-minute slot alignment and payload generators
│   └── summary_reporter.js         # Lifecycle hook exporting aggregated metrics to JSON
├── scenarios/
│   ├── 01_average_load_read.js     # Type 1: High-throughput read path (Catalog & Availability)
│   └── 02_spike_stress_order.js    # Type 2: Spike/stress transactional preorder concurrency
├── reports/                        # JSON summary test reports
│   ├── summary-average-load.json
│   └── summary-spike-stress.json
├── run_load_tests.ps1              # Automated PowerShell runner
└── README.md                       # Documentation & metric interpretation guide
```

---

## 🎯 Test Types & Scenarios

### 1. Type 1: Average Load Test (`01_average_load_read.js`)
- **Focus**: Evaluates steady-state customer browsing behavior on the public read path.
- **Target Endpoints**:
  - `GET /api/v1/flavors` (Catalog Service via Redis cache)
  - `GET /api/v1/inventory/availability` (Batch Inventory Service)
- **Profile**:
  - Ramp-up: 15s to 20 VUs
  - Plateau: 30s at 20 VUs
  - Ramp-down: 10s to 0 VUs
- **Thresholds**: $p(95) < 250\text{ms}$, $p(99) < 600\text{ms}$, Failure Rate $< 1\%$.

### 2. Type 2: Spike / Stress Test (`02_spike_stress_order.js`)
- **Focus**: Simulates a sudden flash-sale rush when preorders open for limited artisanal gelato batches.
- **Target Endpoint**:
  - `POST /api/v1/orders` (Order Service $\rightarrow$ Batch Inventory Service gRPC FEFO allocation)
- **Profile**:
  - Warm-up: 5s at 10 VUs
  - Spike: 10s surging to 50 concurrent ordering VUs
  - Contention: 20s sustained at 50 VUs
  - Cooldown: 15s ramp-down
- **Thresholds**: Server 5xx error rate $= 0\%$, $p(95) < 2500\text{ms}$. (HTTP 409 `INSUFFICIENT_STOCK` is tracked as acceptable business rejection when stock is exhausted).

---

## 🚀 How to Run

### Automated Execution (PowerShell)
```powershell
# Run all scenarios sequentially:
.\load-tests\run_load_tests.ps1 -Scenario all

# Run only the Average Load test:
.\load-tests\run_load_tests.ps1 -Scenario average

# Run only the Spike / Stress test:
.\load-tests\run_load_tests.ps1 -Scenario spike
```

### Direct k6 CLI Execution
```powershell
# Type 1:
& "C:\Program Files\k6\k6.exe" run load-tests/scenarios/01_average_load_read.js

# Type 2:
& "C:\Program Files\k6\k6.exe" run load-tests/scenarios/02_spike_stress_order.js
```

---

## 📊 JSON Report Output

Every scenario automatically generates a structured JSON report inside `load-tests/reports/`:
- `load-tests/reports/summary-average-load.json`
- `load-tests/reports/summary-spike-stress.json`

### Key Metrics to Examine in the JSON File:
1. `metrics.http_req_duration.values["p(95)"]`: 95th percentile latency (95% of requests finished faster than this value).
2. `metrics.http_reqs.values.rate`: Throughput (Requests Per Second - RPS).
3. `metrics.http_req_failed.values.rate`: Error percentage.
4. `root_group.checks`: Logical verification assertions (e.g. status 200, valid JSON payload).
