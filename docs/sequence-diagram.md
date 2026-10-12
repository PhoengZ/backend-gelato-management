# Order checkout and reservation sequence diagrams

Order handlers and outbound clients and Payment's authenticated
CreateCheckoutSession server/Stripe adapter are implemented. Payment's Checkout
webhook adapter is pending; webhook/callback steps below describe the selected
integration target. Deployed end-to-end integration remains unverified.
Contracts under ../contracts/ remain authoritative.

## Checkout and successful payment

```mermaid
sequenceDiagram
    autonumber
    actor C as Customer Web
    participant G as API Gateway
    participant O as Order Service
    participant Cat as Catalog Service
    participant I as Batch Inventory
    participant DB as Order PostgreSQL
    participant P as Payment Service
    participant S as Stripe
    participant W as Outbox Worker
    participant R as RabbitMQ

    C->>G: POST /api/v1/orders with items, pickup_at, Idempotency-Key
    G->>O: Same versioned path, verified X-User-ID and original JWT
    O->>DB: Lookup customer plus key and request hash
    Note over O,DB: Diagram continues for a new checkout
    O->>Cat: gRPC BatchGetFlavors
    Cat-->>O: Active flavors, names and minor-unit prices
    Note over O: Validate prices and snapshot checkout total
    O->>I: gRPC ReservePortions with stable order ID and key
    I-->>O: Reservation ID and expires_at
    O->>DB: Transaction: PENDING_PAYMENT order and items
    Note over P,S: Checkout Session gRPC and Stripe adapter implemented
    O->>P: gRPC CreateCheckoutSession with total and stable checkout key
    P->>S: Create or retrieve Checkout Session
    S-->>P: Session ID and HTTPS checkout URL
    P-->>O: checkout_session_id and checkout_url
    O->>DB: Lock order, recheck pending and expiry, bind session
    O-->>G: 201 order fields plus checkout_url, Cache-Control no-store
    G-->>C: Checkout response
    C->>S: Redirect to checkout_url and pay

    S->>G: Signed webhook, raw body and Stripe-Signature
    G->>P: Forward unchanged webhook bytes and signature
    Note over P,O: Checkout webhook reporting to Order still pending
    Note over P: Verify provider result and correlate it to the order
    P->>O: POST /api/v1/orders/{id}/payment-succeeded with service token
    O->>DB: Transaction: claim PAYMENT_CONFIRMING
    O->>I: gRPC ConfirmReservation with stable confirmation key
    I-->>O: CONFIRMED
    O->>DB: Transaction: PAID and order.placed outbox
    O-->>P: 204 after commit
    W->>DB: Read pending outbox
    W->>R: Publish CloudEvent to order exchange
    R-->>W: Publisher confirmation
    W->>DB: Mark published

    Note over C,S: Browser return and webhook can arrive in either order
    C->>G: GET /api/v1/orders/{id} after returning
    G->>O: Authenticated order lookup
    O-->>G: Current order state
    G-->>C: Poll while payment is pending
```

Session creation and a success redirect never mark an order paid. Order's
publisher matches confirmations to the current delivery tag, rejects mandatory
returns and reconnects after failures. Broker runtime integration remains
unverified; ambiguous failures can deliver duplicate events.

The legacy PaymentIntent flow uses a separate frontend Payment REST call,
`client_secret`, and Payment-to-Order `GetOrderDetails`, `BindPaymentIntent` and
`ReportPaymentResult` RPCs. Order does not implement that gRPC server, so it is
not part of the selected Checkout Session sequence. See
[Payment integration](architecture/payment-integration.md) for migration details.

## Retry after Payment timeout or unavailable response

```mermaid
sequenceDiagram
    actor F as Frontend through Gateway
    participant O as Order
    participant DB as Order PostgreSQL
    participant P as Payment

    Note over O,DB: Order and bounded reservation already persisted
    O->>P: CreateCheckoutSession with checkout key
    P--xO: Timeout or unavailable, provider outcome may be ambiguous
    O-->>F: 504 or 503 with details.order_id
    F->>O: Retry exact body and original Idempotency-Key
    O->>DB: Find matching existing order
    alt Session already bound
        O->>DB: Recheck pending state and expiry under lock
        O-->>F: Same checkout_url
    else Session not yet bound
        O->>P: CreateCheckoutSession with same checkout key
        P-->>O: Same session and URL
        O->>DB: Bind session before exposing URL
        O-->>F: Checkout response
    end
```

Payment must honor idempotency. Order does not release the hold on a Payment
timeout or automatically retry that mutation. A changed request with the same key
returns 409. An expired hold returns 409; a new checkout requires a new key.

## Customer cancellation and payment failure

```mermaid
sequenceDiagram
    actor F as Frontend through Gateway
    participant P as Payment
    participant O as Order
    participant I as Batch Inventory
    participant DB as Order PostgreSQL

    alt Customer cancels
        F->>O: POST /api/v1/orders/{id}/cancel, reason CUSTOMER_REQUEST
        Note over O: Verify customer ownership
    else Verified payment failure
        P->>O: POST /api/v1/orders/{id}/payment-failed with service token
    end
    Note over O: Only pending orders can cancel, CANCELLED replay is a no-op
    O->>DB: Transaction: claim CANCEL_PENDING and clear checkout URL
    Note over O: Checkout and opposing payment claim are blocked
    O->>I: ReleaseReservation with stable release key and ORDER_CANCELLED
    alt Allocated batch still valid
        Note over I: reserved portions become available
    else Allocated batch expired
        Note over I: reserved portions become waste
    end
    I-->>O: RELEASED or EXPIRED
    O->>DB: Transaction: CANCELLED and order.cancelled outbox
    Note over O: Return 204 to the caller after commit
```

An Inventory error prevents final cancellation commit, while CANCEL_PENDING
remains durable. If release succeeds but Order's database commit fails, retries
or the lifecycle worker use the same Inventory key and constant reason. Order currently does
not expire/cancel Stripe sessions; payment through an old URL requires
reconciliation/refund and must not revive a cancelled order.

## Expiry and late payment

```mermaid
sequenceDiagram
    participant Sweep as Inventory Expiry Worker
    participant I as Inventory
    participant P as Payment
    participant O as Order

    Sweep->>I: Expire ACTIVE reservation at stock deadline
    Note over I: Return valid portions or record expired-batch waste
    Note over O: No Order expiry worker, local state may remain pending
    P->>O: Verified late payment success
    O->>I: ConfirmReservation
    I-->>O: FAILED_PRECONDITION
    O-->>P: 409 conflict, no PAID transition or order.placed
    Note over P: Durable reconciliation and refund integration required
```

## Order state and downstream targets

```mermaid
stateDiagram-v2
    [*] --> PENDING_PAYMENT: Reservation and order persisted
    PENDING_PAYMENT --> PAYMENT_CONFIRMING: Verified success claims workflow
    PAYMENT_CONFIRMING --> PAID: Inventory confirmation and local outbox commit
    PAYMENT_CONFIRMING --> CANCEL_PENDING: Inventory definitively rejects confirmation
    PENDING_PAYMENT --> CANCEL_PENDING: Customer cancel or verified payment failure claims workflow
    CANCEL_PENDING --> CANCELLED: Inventory release and local outbox commit
    PAID --> PREPARING: Target fulfillment integration
    PREPARING --> READY_FOR_PICKUP: Target fulfillment integration
    READY_FOR_PICKUP --> COMPLETED: Target collection integration
    CANCELLED --> [*]
    COMPLETED --> [*]

    note right of PREPARING
        Later states exist in the model.
        This branch has no transition handlers for them.
    end note
```

Fulfillment's queue allocation and Notification's SSE/event consumer are
implemented; their Gateway and broker runtime integration remains to be verified.
Notification sends queue numbers on order.ready and keeps only a bounded,
in-memory order-owner map from order.placed. A never-paid cancellation may have
no owner in that map and must not be interpreted as reversal of a paid sale.
Analytics' GetOrderDetails and StreamOrderItems contracts exist, but
Order does not implement its server. Order accepts pickup_at, not a pickup-slot
ID, and its response does not allocate a queue number.
