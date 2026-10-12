# Gelato Management System - Frontend Demo API Reference

## Overview

This document records the current frontend demo interface. It is retained as a
migration reference and is not the canonical backend contract.

The versioned OpenAPI documents under `contracts/openapi/`, the Inventory gRPC
definition under `contracts/proto/`, and the JSON Schemas under
`contracts/events/` are the backend sources of truth. When this demo reference
differs from those contracts, service and Gateway implementations follow the
versioned contracts and the frontend is migrated separately.

Key migration differences include:

| Frontend demo | Canonical backend contract |
| --- | --- |
| `/api/v1/catalog/flavors` | `/api/v1/flavors` |
| Catalog flavor contains stock | Catalog contains metadata only; Inventory owns availability |
| Prefixed IDs and camelCase | UUID IDs and `snake_case` JSON |
| Decimal or ambiguous money | Integer `*_minor` plus `currency` |
| Cookie session inside the demo | Gateway validates Auth Service bearer JWT; an edge layer may store it in an `httpOnly` cookie |
| Frontend creates a PaymentIntent separately | Frontend calls Order; Order calls Payment CreateCheckoutSession and returns checkout_url |
| X-Idempotency-Key / body idempotencyKey | UUID Idempotency-Key header; reuse it with the exact request on retry |
| POST Order immediately returns PAID and queueNumber | Returns PENDING_PAYMENT and checkout_url after session binding; queue allocation is separate |
| Public GET /orders/{id}/status | Authenticated GET /api/v1/orders/{id}; Order currently supplies no queue number |

The API handles authentication, order management, inventory tracking, sales
analytics, and payment processing for the demo UI.

**API Base URL**: `${NEXT_PUBLIC_API_BASE_URL}`

**Framework**: Next.js API Routes (Server-side)

**Client Library**: TanStack React Query

---

## Table of Contents

1. [Authentication](#authentication)
2. [Catalog (Flavors)](#catalog-flavors)
3. [Orders](#orders)
4. [Fulfillment (Staff)](#fulfillment-staff)
5. [Inventory](#inventory)
6. [Analytics](#analytics)
7. [Payment (Stripe)](#payment-stripe)
8. [Error Handling](#error-handling)
9. [Data Types](#data-types)

---

## Authentication

The current demo stores its session in an `httpOnly` cookie. The canonical
backend interface uses `Authorization: Bearer <JWT>` at API Gateway. A future
frontend edge adapter may keep the JWT in an `httpOnly` cookie and translate it
to the bearer header without changing the service contract.

### POST /api/v1/auth/login

Login user with email and password.

**Access**: Public

**Request Body**:
```json
{
  "email": "user@example.com",
  "password": "password123"
}
```

**Response** (201):
```json
{
  "user": {
    "id": "usr_123",
    "name": "John Doe",
    "email": "user@example.com",
    "role": "CUSTOMER"
  },
  "expiresAt": "2026-09-02T15:00:00.000Z"
}
```

**Error Responses**:
- `400 INVALID_JSON` - Malformed request body
- `401 INVALID_CREDENTIALS` - Wrong email or password

**Frontend Hook**: `useLogin()`

---

### POST /api/v1/auth/signup

Register a new user account.

**Access**: Public

**Request Body**:
```json
{
  "name": "John Doe",
  "email": "newuser@example.com",
  "password": "password123"
}
```

**Response** (201):
```json
{
  "user": {
    "id": "usr_456",
    "name": "John Doe",
    "email": "newuser@example.com",
    "role": "CUSTOMER"
  },
  "expiresAt": "2026-09-02T15:00:00.000Z"
}
```

**Error Responses**:
- `400 INVALID_JSON` - Malformed request body or validation error

**Frontend Hook**: `useSignup()`

---

### GET /api/v1/auth/me

Get current authenticated user session.

**Access**: Protected (Any authenticated user)

**Response** (200):
```json
{
  "user": {
    "id": "usr_123",
    "name": "John Doe",
    "email": "user@example.com",
    "role": "CUSTOMER"
  },
  "expiresAt": "2026-09-02T15:00:00.000Z"
}
```

**Error Responses**:
- `401 Unauthorized` - No valid session cookie

**Frontend Hook**: `useSession()`

**Refetch Strategy**: `retry: false`

---

### POST /api/v1/auth/logout

Logout current user and clear session.

**Access**: Protected (Any authenticated user)

**Request Body**: Empty

**Response** (200):
```json
{
  "success": true
}
```

**Frontend Hook**: `useLogout()`

---

## Catalog (Flavors)

### GET /api/v1/catalog/flavors

Retrieve all available gelato flavors.

**Access**: Public

**Query Parameters**: None

**Response** (200):
```json
[
  {
    "id": "flv_vanilla",
    "name": "Vanilla",
    "description": "Classic vanilla flavor",
    "pricePerPortion": 50,
    "allergens": ["dairy", "vanilla"],
    "availablePortions": 100,
    "imageUrl": "/flavors/vanilla.jpg",
    "isAvailable": true
  },
  {
    "id": "flv_chocolate",
    "name": "Chocolate",
    "description": "Rich dark chocolate",
    "pricePerPortion": 60,
    "allergens": ["dairy", "chocolate"],
    "availablePortions": 75,
    "imageUrl": "/flavors/chocolate.jpg",
    "isAvailable": true
  }
]
```

**Caching**: 
- Stale Time: 10 seconds
- Refetch on window focus: Disabled
- Retry: 1 attempt on failure

**Frontend Hook**: `useCatalog()`

---

### POST /api/v1/catalog/flavors

Create a new flavor (Manager only).

**Access**: Protected (MANAGER role)

**Request Body**:
```json
{
  "name": "Pistachio",
  "description": "Creamy pistachio flavor",
  "pricePerPortion": 70,
  "allergens": ["dairy", "nuts"],
  "availablePortions": 50,
  "imageUrl": "/flavors/pistachio.jpg",
  "isAvailable": true
}
```

**Response** (201):
```json
{
  "id": "flv_pistachio",
  "name": "Pistachio",
  "description": "Creamy pistachio flavor",
  "pricePerPortion": 70,
  "allergens": ["dairy", "nuts"],
  "availablePortions": 50,
  "imageUrl": "/flavors/pistachio.jpg",
  "isAvailable": true
}
```

**Error Responses**:
- `400 INVALID_JSON` - Malformed request
- `401 Unauthorized` - No authentication
- `403 Forbidden` - Insufficient permissions (not MANAGER)

**Cache Invalidation**: Invalidates `catalog` and `inventory` queries

**Frontend Hook**: `useCreateFlavor()`

---

### PATCH /api/v1/catalog/flavors/[flavorId]

Update an existing flavor (Manager only).

**Access**: Protected (MANAGER role)

**URL Parameters**:
- `flavorId` (string) - The flavor ID to update

**Request Body**:
```json
{
  "name": "Pistachio Premium",
  "description": "Premium creamy pistachio",
  "pricePerPortion": 75,
  "allergens": ["dairy", "nuts"],
  "availablePortions": 60,
  "imageUrl": "/flavors/pistachio-premium.jpg",
  "isAvailable": true
}
```

**Response** (200):
```json
{
  "id": "flv_pistachio",
  "name": "Pistachio Premium",
  "description": "Premium creamy pistachio",
  "pricePerPortion": 75,
  "allergens": ["dairy", "nuts"],
  "availablePortions": 60,
  "imageUrl": "/flavors/pistachio-premium.jpg",
  "isAvailable": true
}
```

**Cache Invalidation**: Invalidates `catalog`, `inventory`, and `analytics` queries

**Frontend Hook**: `useUpdateFlavor()`

---

### DELETE /api/v1/catalog/flavors/[flavorId]

Delete a flavor (Manager only).

**Access**: Protected (MANAGER role)

**URL Parameters**:
- `flavorId` (string) - The flavor ID to delete

**Request Body**: Empty

**Response** (200):
```json
{
  "id": "flv_pistachio",
  "name": "Pistachio Premium",
  "description": "Premium creamy pistachio",
  "pricePerPortion": 75,
  "allergens": ["dairy", "nuts"],
  "availablePortions": 60,
  "imageUrl": "/flavors/pistachio-premium.jpg",
  "isAvailable": true
}
```

**Cache Invalidation**: Invalidates `catalog`, `inventory`, and `analytics` queries

**Frontend Hook**: `useDeleteFlavor()`

---

## Orders

### Current backend checkout migration

The selected workflow is Frontend -> Gateway -> Order -> Payment -> Stripe.
Order's current contract is [Order OpenAPI v1.1](../contracts/openapi/order-service.v1.yaml).
The outbound Payment client exists, but the Payment Checkout Session server and
webhook adapter are pending. Do not treat the legacy examples below as compatible
backend requests or claim the complete Stripe workflow is running.

Send an Auth JWT to Gateway and a UUID `Idempotency-Key`. Gateway must derive
`X-User-ID` from verified claims; browser-supplied identity headers are not trusted.
The body uses UUID flavor IDs, `pickup_at` and `snake_case`:

```json
{
  "pickup_at": "2026-10-11T12:00:00Z",
  "items": [
    {
      "flavor_id": "0f3bca11-1eb2-4a86-908a-e60d7656c79b",
      "portions": 2
    }
  ]
}
```

Use a future pickup time at runtime. Order obtains the price from Catalog and
reserves stock with Inventory. A successful new checkout returns 201 with
`Cache-Control: no-store` and all order fields, including:

```json
{
  "order_id": "8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594",
  "customer_id": "8dc81c31-a354-4774-b835-36ea0411bc54",
  "pickup_at": "2026-10-11T12:00:00Z",
  "status": "PENDING_PAYMENT",
  "reservation_expires_at": "2026-10-11T11:10:00Z",
  "total_amount_minor": 15000,
  "currency": "THB",
  "items": [
    {
      "flavor_id": "0f3bca11-1eb2-4a86-908a-e60d7656c79b",
      "flavor_name": "Vanilla",
      "portions": 2,
      "unit_price_minor": 7500,
      "subtotal_minor": 15000
    }
  ],
  "created_at": "2026-10-11T11:00:00Z",
  "updated_at": "2026-10-11T11:00:01Z",
  "checkout_url": "https://checkout.stripe.com/..."
}
```

Frontend opens `checkout_url`, then reads `GET /api/v1/orders/{order_id}` after
returning. GET requires customer authentication/ownership and omits the checkout
URL. Continue polling for CANCEL_PENDING or PAYMENT_CONFIRMING; neither returns
a checkout URL. Polling the payment-result page can stop on PAID or CANCELLED; a pending
state can outlive its Inventory reservation because Order has no expiry worker.
Do not infer PAID from Stripe's browser redirect.

For cancellation, call `POST /api/v1/orders/{order_id}/cancel` with
`{"reason":"CUSTOMER_REQUEST"}`. Order first durably claims CANCEL_PENDING, then
releases Inventory with ORDER_CANCELLED before committing CANCELLED. Its lifecycle
worker resumes unfinished work after restart. Valid-batch portions become available and expired-batch portions become
waste. Cancellation currently does not close the Stripe session. Paid orders
cannot use this pending-order cancellation endpoint.

On Payment 503/504, keep the exact request and original key, then retry. The error
includes `details.order_id`. Same key with changed data, or an expired hold, returns
409. A terminal-order replay returns current order fields without a checkout URL.

See [checkout architecture](architecture/order-checkout.md) and
[sequence diagrams](sequence-diagram.md) for full failure/callback behavior.

### Legacy demo: POST /api/v1/orders

Create a new order.

**Access**: Protected (CUSTOMER role)

**Headers**:
- `X-Idempotency-Key` (string) - Unique key for deduplication

**Request Body**:
```json
{
  "items": [
    {
      "flavorId": "flv_vanilla",
      "portions": 2
    },
    {
      "flavorId": "flv_chocolate",
      "portions": 1
    }
  ],
  "paymentMethod": "PROMPTPAY_MOCK",
  "idempotencyKey": "ord_abc123_12345"
}
```

**Response** (201):
```json
{
  "orderId": "ord_12345",
  "queueNumber": "A-042",
  "status": "PAID",
  "createdAt": "2026-09-02T10:30:00.000Z"
}
```

**Error Responses**:
- `400 INVALID_JSON` - Malformed request
- `400 OUT_OF_STOCK` - Requested flavor/portions not available
- `401 Unauthorized` - No authentication

**Idempotency**: Uses `X-Idempotency-Key` header to prevent duplicate orders within a time window

**Frontend Hook**: `useCreateOrder()`

---

### Legacy demo: GET /api/v1/orders/[orderId]/status

Get order status and queue information.

**Access**: Public

**URL Parameters**:
- `orderId` (string) - The order ID

**Response** (200):
```json
{
  "orderId": "ord_12345",
  "queueNumber": "A-042",
  "status": "PREPARING",
  "createdAt": "2026-09-02T10:30:00.000Z",
  "estimatedWaitMinutes": 7
}
```

**Polling**: Refetches every 3 seconds until status is `PICKED_UP`, then stops

**Error Responses**:
- `404 Not Found` - Order doesn't exist

**Frontend Hook**: `useQueueStatus(orderId: string)`

---

## Fulfillment (Staff)

### GET /api/v1/fulfillments

Retrieve orders by status for staff (Staff/Manager only).

**Access**: Protected (STAFF or MANAGER role)

**Query Parameters**:
- `status` (string) - Comma-separated statuses: `PREPARING,READY`

**Example**: `/api/v1/fulfillments?status=PREPARING,READY`

**Response** (200):
```json
[
  {
    "orderId": "ord_12345",
    "queueNumber": "A-042",
    "status": "PREPARING",
    "createdAt": "2026-09-02T10:30:00.000Z",
    "estimatedWaitMinutes": 5,
    "items": [
      {
        "flavorId": "flv_vanilla",
        "flavorName": "Vanilla",
        "portions": 2
      },
      {
        "flavorId": "flv_chocolate",
        "flavorName": "Chocolate",
        "portions": 1
      }
    ]
  }
]
```

**Polling**: Refetches every 2 seconds

**Error Responses**:
- `401 Unauthorized` - No authentication
- `403 Forbidden` - Insufficient permissions

**Frontend Hook**: `useFulfillments(statuses?: OrderStatus[])`

---

### PATCH /api/v1/fulfillments/[orderId]/status

Update order status to READY or PICKED_UP (Staff/Manager only).

**Access**: Protected (STAFF or MANAGER role)

**URL Parameters**:
- `orderId` (string) - The order ID to update

**Request Body**:
```json
{
  "status": "READY"
}
```

**Valid Status Transitions**:
- `PREPARING` → `READY`
- `READY` → `PICKED_UP`

**Response** (200):
```json
{
  "orderId": "ord_12345",
  "queueNumber": "A-042",
  "status": "READY",
  "createdAt": "2026-09-02T10:30:00.000Z",
  "estimatedWaitMinutes": 0,
  "items": [
    {
      "flavorId": "flv_vanilla",
      "flavorName": "Vanilla",
      "portions": 2
    }
  ]
}
```

**Cache Invalidation**: 
- Updates `queue-status` query cache
- Invalidates `fulfillments` query

**Error Responses**:
- `400 Invalid transition` - Invalid status update
- `401 Unauthorized` - No authentication
- `403 Forbidden` - Insufficient permissions
- `404 Not Found` - Order doesn't exist

**Frontend Hook**: `useUpdateOrderStatus()`

---

## Inventory

### GET /api/v1/inventory

Get current inventory snapshot (Manager only).

**Access**: Protected (MANAGER role)

**Response** (200):
```json
{
  "flavors": [
    {
      "id": "flv_vanilla",
      "name": "Vanilla",
      "description": "Classic vanilla flavor",
      "pricePerPortion": 50,
      "allergens": ["dairy"],
      "availablePortions": 100,
      "imageUrl": "/flavors/vanilla.jpg",
      "isAvailable": true
    }
  ],
  "batches": [
    {
      "id": "btch_001",
      "batchCode": "VAN-2026-09-02-001",
      "flavorId": "flv_vanilla",
      "flavorName": "Vanilla",
      "producedAt": "2026-09-02T08:00:00.000Z",
      "expiresAt": "2026-09-05T08:00:00.000Z",
      "initialPortions": 200,
      "remainingPortions": 150
    }
  ],
  "waste": [
    {
      "id": "wst_001",
      "batchId": "btch_001",
      "flavorId": "flv_vanilla",
      "flavorName": "Vanilla",
      "portions": 50,
      "reason": "Expiration",
      "createdAt": "2026-09-02T14:00:00.000Z"
    }
  ]
}
```

**Error Responses**:
- `401 Unauthorized` - No authentication
- `403 Forbidden` - Not a manager

**Frontend Hook**: `useInventory()`

---

### POST /api/v1/inventory/batches

Create a new production batch (Manager only).

**Access**: Protected (MANAGER role)

**Request Body**:
```json
{
  "flavorId": "flv_vanilla",
  "batchCode": "VAN-2026-09-02-001",
  "portions": 200,
  "producedAt": "2026-09-02T08:00:00.000Z",
  "expiresAt": "2026-09-05T08:00:00.000Z"
}
```

**Response** (201):
```json
{
  "id": "btch_001",
  "batchCode": "VAN-2026-09-02-001",
  "flavorId": "flv_vanilla",
  "flavorName": "Vanilla",
  "producedAt": "2026-09-02T08:00:00.000Z",
  "expiresAt": "2026-09-05T08:00:00.000Z",
  "initialPortions": 200,
  "remainingPortions": 200
}
```

**Cache Invalidation**: Invalidates `inventory` and `catalog` queries

**Error Responses**:
- `400 INVALID_JSON` - Malformed request
- `401 Unauthorized` - No authentication
- `403 Forbidden` - Not a manager

**Frontend Hook**: `useCreateBatch()`

---

### PATCH /api/v1/inventory/flavors/[flavorId]

Update flavor inventory and allergens (Manager only).

**Access**: Protected (MANAGER role)

**URL Parameters**:
- `flavorId` (string) - The flavor ID

**Request Body**:
```json
{
  "availablePortions": 150,
  "allergens": ["dairy", "nuts", "vanilla"]
}
```

**Response** (200):
```json
{
  "id": "flv_vanilla",
  "name": "Vanilla",
  "description": "Classic vanilla flavor",
  "pricePerPortion": 50,
  "allergens": ["dairy", "nuts", "vanilla"],
  "availablePortions": 150,
  "imageUrl": "/flavors/vanilla.jpg",
  "isAvailable": true
}
```

**Cache Invalidation**: Invalidates `inventory` and `catalog` queries

**Error Responses**:
- `400 INVALID_JSON` - Malformed request
- `401 Unauthorized` - No authentication
- `403 Forbidden` - Not a manager

**Frontend Hook**: `useUpdateFlavorInventory()`

---

### POST /api/v1/inventory/waste

Record waste or spoilage (Manager only).

**Access**: Protected (MANAGER role)

**Request Body**:
```json
{
  "batchId": "btch_001",
  "portions": 50,
  "reason": "Expiration"
}
```

**Response** (201):
```json
{
  "id": "wst_001",
  "batchId": "btch_001",
  "flavorId": "flv_vanilla",
  "flavorName": "Vanilla",
  "portions": 50,
  "reason": "Expiration",
  "createdAt": "2026-09-02T14:00:00.000Z"
}
```

**Cache Invalidation**: Invalidates `inventory`, `catalog`, and `analytics` queries

**Error Responses**:
- `400 INVALID_JSON` - Malformed request
- `401 Unauthorized` - No authentication
- `403 Forbidden` - Not a manager

**Frontend Hook**: `useRecordWaste()`

---

## Analytics

### GET /api/v1/analytics/summary

Get sales and production analytics (Manager only).

**Access**: Protected (MANAGER role)

**Response** (200):
```json
{
  "totalRevenue": 15000,
  "totalOrders": 150,
  "totalScoops": 450,
  "totalWaste": 80,
  "salesByFlavor": [
    {
      "flavorId": "flv_vanilla",
      "flavorName": "Vanilla",
      "portions": 200,
      "revenue": 10000
    },
    {
      "flavorId": "flv_chocolate",
      "flavorName": "Chocolate",
      "portions": 150,
      "revenue": 9000
    }
  ],
  "wasteByFlavor": [
    {
      "flavorId": "flv_vanilla",
      "flavorName": "Vanilla",
      "portions": 50
    }
  ],
  "salesTrend": [
    {
      "date": "2026-09-01",
      "label": "Sep 1",
      "revenue": 5000,
      "orders": 50,
      "scoops": 150
    },
    {
      "date": "2026-09-02",
      "label": "Sep 2",
      "revenue": 10000,
      "orders": 100,
      "scoops": 300
    }
  ]
}
```

**Error Responses**:
- `401 Unauthorized` - No authentication
- `403 Forbidden` - Not a manager

**Frontend Hook**: `useAnalytics()`

---

## Payment (Stripe)

The selected backend checkout returns a Stripe Checkout URL through Order.
Frontend does not call Payment separately or submit its own amount. Payment
implements the [internal checkout gRPC API](../contracts/proto/payment/v1/payment.proto)
and Stripe Checkout adapter. Checkout webhook reporting to Order's HTTP callbacks
and end-to-end verification remain pending.

The retained PaymentIntent endpoint is defined in
`contracts/openapi/payment-service.v1.yaml`: it accepts `order_id`, obtains the
amount/currency from Order and returns `client_secret`, `payment_intent_id`,
`amount_minor` and `currency`. Client amounts and camelCase aliases are rejected.
That path depends on Order gRPC methods whose server is not implemented; see
[Payment integration](architecture/payment-integration.md) for migration work.

The amount/clientSecret examples below are preserved legacy demo shapes; neither
the current Order checkout nor the Payment branch accepts client-selected totals.

### Legacy demo: POST /api/v1/payments/create-payment-intent

Create a Stripe payment intent for checkout (Customer only).

**Access**: Protected (CUSTOMER role)

**Request Body**:
```json
{
  "amount": 250
}
```

**Response** (201):
```json
{
  "clientSecret": "pi_1234567890_secret_abcdefg"
}
```

**Error Responses**:
- `400 INVALID_JSON` - Malformed request
- `401 Unauthorized` - No authentication
- `500 Payment error` - Stripe API error

**Frontend Usage**: 
```typescript
const res = await fetch("/api/v1/payments/create-payment-intent", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ amount: total })
});
const data = await res.json();
```

---

### POST /api/v1/payments/webhook

Stripe webhook endpoint for payment events (Server-to-server).

**Access**: Public (Stripe-signed)

**Webhook Events Handled**:

The list below describes the legacy PaymentIntent flow. The Checkout adapter must
verify signed provider results, correlate session/order metadata, handle supported
payment methods and report success only once payment is confirmed. The Order
branch supplies authenticated HTTP success/failure callbacks; it does not verify
Stripe webhooks itself. Browser return URLs must never call these callbacks.

- `payment_intent.succeeded` - Payment completed
- `payment_intent.payment_failed` - Payment failed

**Response** (200):
```json
{
  "received": true
}
```

**Verification**: Stripe webhook signature verification required in header `Stripe-Signature`

---

## Error Handling

### Error Response Format

All errors follow a consistent format:

```json
{
  "message": "User-friendly error message in Thai/English",
  "code": "ERROR_CODE"
}
```

### Common Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `NETWORK_ERROR` | N/A | Connection failed |
| `INVALID_JSON` | 400 | Malformed JSON request |
| `INVALID_CREDENTIALS` | 401 | Wrong email/password |
| `HTTP_401` | 401 | Unauthorized/Missing auth |
| `HTTP_400` | 400 | Bad request |
| `OUT_OF_STOCK` | 400 | Flavor/portions unavailable |
| `UNKNOWN_ERROR` | 500 | Unexpected server error |

### Error Handling in Frontend

```typescript
try {
  const data = await catalog.requestJson("/api/v1/orders", {...});
} catch (error) {
  if (error instanceof ApiClientError) {
    console.error(error.code, error.status, error.message);
  }
}
```

---

## Data Types

### Flavor

```typescript
interface Flavor {
  id: string;
  name: string;
  description: string;
  pricePerPortion: number;
  allergens: string[];
  availablePortions: number;
  imageUrl: string;
  isAvailable: boolean;
}
```

### Order

Current backend request and response types follow Order OpenAPI. Monetary values
are integer THB minor units; keep the Idempotency-Key in the header, not the body.

```typescript
interface BackendOrderRequest {
  pickup_at: string;
  items: { flavor_id: string; portions: number }[];
}

type BackendOrderStatus =
  | "PENDING_PAYMENT" | "CANCEL_PENDING" | "PAYMENT_CONFIRMING" | "PAID" | "CANCELLED"
  | "PREPARING" | "READY_FOR_PICKUP" | "COMPLETED";

interface BackendOrderResponse {
  order_id: string;
  customer_id: string;
  pickup_at: string;
  status: BackendOrderStatus;
  reservation_expires_at: string;
  total_amount_minor: number;
  currency: "THB";
  items: {
    flavor_id: string;
    flavor_name: string;
    portions: number;
    unit_price_minor: number;
    subtotal_minor: number;
  }[];
  created_at: string;
  updated_at: string;
}

interface BackendCheckoutResponse extends BackendOrderResponse {
  checkout_url?: string; // Present for a payable pending checkout; omitted by GET.
}
```

The following types describe the legacy demo and require migration. The later
fulfillment states above exist in the current model but have no Order transition
handlers yet; queue and staff types below describe demo/target integration only.

```typescript
interface OrderRequest {
  items: OrderItemRequest[];
  paymentMethod: "PROMPTPAY_MOCK";
  idempotencyKey: string;
}

interface OrderItemRequest {
  flavorId: string;
  portions: number;
}

interface OrderResponse {
  orderId: string;
  queueNumber: string;
  status: "PAID" | "PREPARING" | "READY" | "PICKED_UP";
  createdAt: string;
}

interface QueueStatusResponse extends OrderResponse {
  estimatedWaitMinutes: number;
}

interface StaffOrderItem {
  flavorId: string;
  flavorName: string;
  portions: number;
}

interface StaffOrder extends QueueStatusResponse {
  items: StaffOrderItem[];
}
```

### Inventory

```typescript
interface InventoryBatch {
  id: string;
  batchCode: string;
  flavorId: string;
  flavorName: string;
  producedAt: string;
  expiresAt: string;
  initialPortions: number;
  remainingPortions: number;
}

interface WasteRecord {
  id: string;
  batchId: string;
  flavorId: string;
  flavorName: string;
  portions: number;
  reason: string;
  createdAt: string;
}

interface InventorySnapshot {
  flavors: Flavor[];
  batches: InventoryBatch[];
  waste: WasteRecord[];
}
```

### Authentication

```typescript
type UserRole = "CUSTOMER" | "STAFF" | "MANAGER";

interface AuthUser {
  id: string;
  name: string;
  email: string;
  role: UserRole;
}

interface AuthSession {
  user: AuthUser;
  expiresAt: string;
}
```

### Analytics

```typescript
interface AnalyticsSummary {
  totalRevenue: number;
  totalOrders: number;
  totalScoops: number;
  totalWaste: number;
  salesByFlavor: Array<{
    flavorId: string;
    flavorName: string;
    portions: number;
    revenue: number;
  }>;
  wasteByFlavor: Array<{
    flavorId: string;
    flavorName: string;
    portions: number;
  }>;
  salesTrend: Array<{
    date: string;
    label: string;
    revenue: number;
    orders: number;
    scoops: number;
  }>;
}
```

### Error

```typescript
interface ApiError {
  message: string;
  code: string;
}

class ApiClientError extends Error implements ApiError {
  code: string;
  status: number;
}
```

---

## Authentication & Authorization

### Cookie-Based Sessions

- Session token stored in `httpOnly` cookie (cannot be accessed by JavaScript)
- Cookie name: `AUTH_COOKIE`
- Properties:
  - `httpOnly: true` - Secure, JavaScript cannot access
  - `sameSite: lax` - CSRF protection
  - `secure: true` - HTTPS only in production
  - `maxAge: 28800` - 8 hours
  - `path: /` - All routes

### Role-Based Access Control

| Role | Permissions |
|------|-------------|
| **CUSTOMER** | View catalog, create orders, track queue, create payment intents |
| **STAFF** | View fulfillments, update order status |
| **MANAGER** | All permissions + manage catalog, inventory, batches, waste, analytics |

### Protected Endpoint Pattern

```typescript
export async function POST(request: NextRequest) {
  try {
    await requireAuth(request, ["MANAGER"]);
    // Handle request
  } catch (error) {
    const authResponse = authErrorResponse(error);
    if (authResponse) return authResponse;
    // Handle other errors
  }
}
```

---

## Rate Limiting & Caching

### React Query Configuration

Default cache configuration (configured in `components/providers.tsx`):

```typescript
{
  queries: {
    staleTime: 10_000,        // Data considered fresh for 10 seconds
    retry: 1,                 // Retry failed requests once
    refetchOnWindowFocus: false // Don't refetch when window regains focus
  }
}
```

### Endpoint-Specific Polling

The legacy demo rows below are retained for migration. The backend payment-result
row uses the current authenticated Order route; it does not supply queue data.

| Endpoint | Interval | Condition |
|----------|----------|-----------|
| `/api/v1/orders/[orderId]/status` | 3 seconds | Stop when status = `PICKED_UP` |
| `/api/v1/orders/{order_id}` (backend payment-result page) | Suggested 3 seconds | Authenticated; stop on PAID/CANCELLED or show expired-hold recovery |
| `/api/v1/fulfillments` | 2 seconds | Continuous |

---

## Implementation Notes

### Idempotency

Backend checkout uses a UUID `Idempotency-Key` header. Generate a key once for a
new checkout and retain it with the original request across timeout retries:

```typescript
const checkoutKey = crypto.randomUUID(); // Retain for retries of this checkout.
const response = await fetch("/api/v1/orders", {
  method: "POST",
  headers: {
    "Authorization": `Bearer ${accessToken}`,
    "Content-Type": "application/json",
    "Idempotency-Key": checkoutKey
  },
  body: JSON.stringify(checkoutRequest) // BackendOrderRequest; no client total.
});
if (!response.ok) {
  throw await response.json(); // Keep the same request/key for recoverable retries.
}
const checkout: BackendCheckoutResponse = await response.json();
if (checkout.checkout_url) window.location.assign(checkout.checkout_url);
```

`accessToken` and `checkoutRequest` are supplied by the authenticated UI. The key
must not be regenerated on each retry. Order derives stable reservation/order and
checkout keys; Payment must implement same-key session retrieval. Expired or
cancelled checkouts require a new order/key, not a new charge on the old order.

Legacy demo idempotency uses a different header and body shape:

```typescript
headers: { "X-Idempotency-Key": order.idempotencyKey }
```

These legacy fields are not compatibility aliases accepted by the backend.

### Mock Database

The legacy frontend demo endpoints use a mock database (`lib/mock-db.ts`):
- No real persistence
- Demo user credentials: `demo@example.com / password`
- Mock data resets on server restart

---

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 1.0.0 | 2026-09-02 | Initial API specification |
| Migration notes | 2026-10-11 | Order-orchestrated Checkout Session flow, snake_case examples, retry/cancel behavior; legacy demo retained |

---

**Last Updated**: October 11, 2026
