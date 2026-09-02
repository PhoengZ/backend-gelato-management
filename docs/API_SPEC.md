# Gelato Management System - API Specification

## Overview

This is the API specification for the Gelato Management System frontend. The API handles authentication, order management, inventory tracking, sales analytics, and payment processing for a gelato shop management platform.

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

All protected endpoints require authentication via cookie-based sessions. The authentication token is stored in an `httpOnly` cookie and cannot be accessed via JavaScript.

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

### POST /api/v1/orders

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

### GET /api/v1/orders/[orderId]/status

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

### POST /api/v1/stripe/create-payment-intent

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
const res = await fetch("/api/v1/stripe/create-payment-intent", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ amount: total })
});
const data = await res.json();
```

---

### POST /api/v1/stripe/webhook

Stripe webhook endpoint for payment events (Server-to-server).

**Access**: Public (Stripe-signed)

**Webhook Events Handled**:
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

| Endpoint | Interval | Condition |
|----------|----------|-----------|
| `/api/v1/orders/[orderId]/status` | 3 seconds | Stop when status = `PICKED_UP` |
| `/api/v1/fulfillments` | 2 seconds | Continuous |

---

## Implementation Notes

### Idempotency

Order creation uses an idempotency key to prevent duplicate orders:

```typescript
headers: { "X-Idempotency-Key": order.idempotencyKey }
```

Generate a unique key per checkout attempt to safely retry failed requests.

### Mock Database

All endpoints use a mock database (`lib/mock-db.ts`) for demonstration:
- No real persistence
- Demo user credentials: `demo@example.com / password`
- Mock data resets on server restart

---

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 1.0.0 | 2026-09-02 | Initial API specification |

---

**Last Updated**: September 2, 2026
