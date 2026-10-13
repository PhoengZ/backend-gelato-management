# GelatoFlow Python E2E Integration Testing Suite

Automated black-box end-to-end integration tests for GelatoFlow backend microservices, testing cross-service business use cases and Non-Functional Requirements (NFRs) through the API Gateway.

---

## 📁 Directory Structure

```text
tests/
├── conftest.py                     # Pytest fixtures, Gateway health checks, actors, markers
├── requirements.txt                # Lightweight Python test dependencies
├── run_tests.ps1                   # Automated runner script for Windows PowerShell
├── README.md                       # Comprehensive guide and documentation
├── helpers/
│   ├── __init__.py
│   ├── api_client.py               # ApiClient, Actor models, pure-Python HS256 JWT minting
│   ├── sse_listener.py             # Thread-safe Server-Sent Events client for /notifications/stream
│   └── stripe_simulator.py         # HMAC-SHA256 Stripe webhook generator & signature calculator
├── test_uc01_preorder_checkout.py  # UC-01: Preorder, inventory hold, Stripe checkout, SSE & analytics
├── test_uc02_kitchen_pickup.py     # UC-02: Kitchen queue, staff RBAC, ticket lifecycle progression
├── test_uc03_batch_inventory.py    # UC-03: Manager batch creation, waste deduction, RBAC isolation
├── test_nfr02_concurrency.py       # NFR-02: 20 concurrent orders on 1 portion (prevent overselling)
└── test_nfr_slot_capacity.py       # NFR: 15-minute pickup time slot capacity limits
```

---

## 🚀 Prerequisites

1. **Python 3.10+**
2. **Docker & Docker Compose** (for running backend microservices and databases)

Install test requirements:
```bash
pip install -r tests/requirements.txt
```

---

## 🏃 Quick Start

### 1. Automated Execution via PowerShell
Run the automated PowerShell runner:
```powershell
# Run with existing running containers:
.\tests\run_tests.ps1

# Automatically start Docker containers, run tests, and keep running:
.\tests\run_tests.ps1 -StartContainers

# Run tests and tear down containers afterwards:
.\tests\run_tests.ps1 -StartContainers -StopContainers
```

### 2. Manual Execution via Pytest
```bash
# Run all tests
pytest tests/ -v

# Run only Use Case 1 (Preorder & Checkout)
pytest tests/test_uc01_preorder_checkout.py -v

# Run only Use Case 2 (Kitchen Fulfillment)
pytest tests/test_uc02_kitchen_pickup.py -v

# Run only Use Case 3 (Batch Inventory & Waste)
pytest tests/test_uc03_batch_inventory.py -v

# Run only NFR-02 Concurrency Test (20 racers on 1 portion)
pytest tests/test_nfr02_concurrency.py -v

# Run by marker
pytest -m uc01 -v
pytest -m nfr02 -v
```

---

## ⚙️ Environment Variables

| Variable | Default Value | Description |
| :--- | :--- | :--- |
| `GATEWAY_URL` | `http://localhost:8080` | Base URL of API Gateway |
| `JWT_SECRET` | `development_only_change_this_secret_32_bytes` | Shared HS256 secret for minting & verifying tokens |
| `JWT_ISSUER` | `gelatoflow-auth` | JWT issuer claim (`iss`) |
| `JWT_AUDIENCE` | `gelatoflow-api` | JWT audience claim (`aud`) |
| `STRIPE_WEBHOOK_SECRET` | `whsec_test_secret_for_integration_testing_12345` | Stripe webhook signing secret |

---

## 🧪 Verified Scenarios

- **UC-01 (Pre-order & Checkout Flow)**:
  - Discovery of active flavors via `GET /api/v1/flavors`.
  - Batch availability check via `GET /api/v1/inventory/availability`.
  - SSE real-time listener subscription on `GET /api/v1/notifications/stream`.
  - Pre-order creation with 15-minute slot alignment and `Idempotency-Key` header.
  - Payment intent / Checkout URL issuance.
  - Stripe webhook simulation via HMAC-SHA256 signature to `/api/v1/payments/webhook`.
  - Order state transition polling to `PAID`.
  - Idempotent request replay verification.

- **UC-02 (Kitchen Fulfillment & Pickup)**:
  - Kitchen queue listing on `GET /api/v1/fulfillments`.
  - RBAC verification (`CUSTOMER` role denied with 403 Forbidden).
  - Status progression: `PREPARING` $\rightarrow$ `READY_FOR_PICKUP` $\rightarrow$ `PICKED_UP`.
  - Backward status transition rejection with 409 Conflict.

- **UC-03 (Batch Inventory & Waste Tracking)**:
  - Manager batch creation on `POST /api/v1/inventory/batches`.
  - RBAC verification (`CUSTOMER` and `STAFF` denied with 403 Forbidden).
  - Waste recording with idempotency key on `POST /api/v1/inventory/batches/{id}/waste`.
  - Exact balance deduction: `available_portions = initial - wasted`.
  - Over-wasting rejection (409 Conflict).

- **NFR-02 (Concurrency & Overselling Protection)**:
  - 20 simultaneous threads racing to purchase 1 portion.
  - Exactly 1 request succeeds (HTTP 201).
  - Remaining 19 requests rejected with HTTP 409 `INSUFFICIENT_STOCK`.
  - Inventory balance strictly non-negative ($\ge 0$).

- **NFR (Time-Slot Capacity Protection)**:
  - Enforces 15-minute pickup time slot capacity (default: 10 orders/slot).
  - Excess order requests in the same slot are rejected with HTTP 409 `SLOT_CAPACITY_EXCEEDED`.
