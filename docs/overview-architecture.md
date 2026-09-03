# GelatoFlow System Architecture Reference

This document serves as a reference for AI agents implementing services within the GelatoFlow system. It outlines the architectural design, microservices, communication protocols, and critical business logic (especially regarding inventory consistency).

## 1. Architectural Overview
GelatoFlow utilizes an **Event-Driven Microservices Architecture**.
* **Source of Truth:** `Batch Inventory Service` is the strict source of truth for stock/inventory.
* **Process Controller:** `Order Service` controls the ordering lifecycle.
* **Data Ownership:** Each service owns its respective database. Direct database access across services is strictly prohibited. Cross-service data access must occur via gRPC or Asynchronous Events.

## 2. Microservices Catalog

| Service | Responsibility | Database |
| :--- | :--- | :--- |
| **API Gateway** | Routing, Authentication, Rate limiting. | *None* |
| **Auth Service** | User management, Roles, JWT issuance. | PostgreSQL |
| **Catalog Service** | Flavors, Prices, Recipes, Allergens. | Redis *(Note: Updated from MongoDB)* |
| **Order Service** | Orders, Customizations, Time-slot reservation, Payment status. | PostgreSQL |
| **Batch Inventory Service** | Batches, Remaining portions, Reservations, Waste, Expiry dates. | PostgreSQL |
| **Fulfillment Service** | Preparation queues, Pickup time-slot management, QR Pickup. | PostgreSQL |
| **Notification Service** | Notifications for ready orders & expiring batches. | MongoDB (or Stateless) |
| **Analytics Service** | Sales summaries, Waste reports, Best-selling flavors (MVP Add-on). | MongoDB |
| **Payment Service** | Payment processing integration (Stripe API) and Webhook handling. | *None* |

## 3. Communication Patterns

### A. REST API (External / Client-to-System)
Used for communication between the Front-end (Customer Web, Staff/Manager Dashboards) and the backend services via the API Gateway.

### B. gRPC (Internal Synchronous)
Used strictly between `Order Service` and `Batch Inventory Service` requiring immediate consistency and fast response times (e.g., checking if a portion can be reserved).
* **Key Operations:**
  * `CheckAvailability`
  * `ReservePortions`
  * `ReleaseReservation`
  * `ConfirmReservation`

### C. Message Broker - RabbitMQ (Internal Asynchronous)
Used for decoupling services and handling asynchronous workflows. (RabbitMQ preferred over Kafka for this scope).
* **Key Events:**
  * `OrderPlaced`
  * `OrderCancelled`
  * `OrderReady`
  * `OrderPickedUp`
  * `BatchLowStock`
  * `BatchExpiring`
  * `WasteRecorded`

## 4. Core Workflows

### Ordering & Time-Slot Queue Flow (Happy Path)
1. **Check Availability & Time Slots:** Customer views real-time flavor stock and available pickup time slots (`GET /api/v1/catalog/flavors` & `GET /api/v1/catalog/timeslots`) via **API Gateway**.
2. **Create Order:** Customer selects flavors and picks a desired time slot, then submits `POST /api/v1/orders`.
3. **Reserve Stock:** **Order Service** calls **Batch Inventory Service** via `gRPC (ReservePortions)` to atomically reserve portions.
4. **Pending Payment:** Order is saved with status `PENDING_PAYMENT` bound to the selected time slot.
5. **Initiate Payment:** Frontend requests payment intent via `POST /api/v1/payments/create-payment-intent`.
6. **Payment Service:** Calls Stripe to create PaymentIntent and returns `clientSecret`.
7. **Direct Payment:** Customer completes card payment securely with Stripe.
8. **Payment Webhook:** Stripe sends `payment_intent.succeeded` webhook to API Gateway (`POST /api/v1/payments/webhook`), which forwards to **Payment Service**, and **Payment Service** instructs **Order Service** to update status to `PAID`.
9. **Publish Event:** **Order Service** publishes `OrderPlaced` event (containing order details & timeslot) to **RabbitMQ**.
10. **Time-Slot Queue Allocation:** 
    * **Fulfillment Service** consumes `OrderPlaced`, allocates a sequential **Queue Number scoped to that specific Time Slot** (e.g., Slot `14:00 - 14:15` -> Queue `#01`), and places it into the kitchen prep queue.
11. **Notify Customer:** **Notification Service** consumes `OrderPlaced` and sends an order confirmation with the **Slot Details & Slot Queue Number** to the Customer.

*(If stock is insufficient at step 3, Batch Inventory returns an Out of Stock error, and Order Service aborts the order).*

### Fulfillment Flow (Storefront & Kitchen)
1. Staff dashboard displays orders grouped and prioritized by **Time Slots** and **Slot Queue Numbers**.
2. Staff prepares gelato according to the time slot schedule.
3. When ready, staff updates status to `READY_FOR_PICKUP` (`PATCH /api/v1/fulfillments/:id/status`).
4. **Fulfillment Service** publishes `OrderReady` event to **RabbitMQ**.
5. **Notification Service** alerts the customer that their order for that time slot is ready.
6. Customer arrives during their time slot, presents their QR/Queue reference, and staff marks order as `COMPLETED`.

## 5. Data Integrity & Overselling Prevention
Preventing overselling is the most critical business requirement.
* **Single Authority:** `Batch Inventory Service` is the sole decision-maker for stock.
* **Mechanisms Required:**
  * **Atomic Database Transactions**
  * **Row Locking or Optimistic Locking**
  * **Idempotency Keys** (to prevent duplicate order processing)
  * **Transactional Outbox Pattern** (to guarantee event publishing after DB commits)
* **Constraints:** `available_portions` must NEVER be negative. Reservations MUST be released if an order is cancelled or payment expires.
* **Inventory Formula:** 
  `Available = Produced - Reserved - Sold - Waste`

## 6. Quality Attributes

### A. Consistency (Primary)
* Strict consistency is required to prevent overselling.
* **Test Case:** If 1 portion of Pistachio remains and 20 concurrent requests arrive, exactly 1 order must succeed, 19 must fail, stock must not drop below 0, and no duplicate reservations should occur.

### B. Performance (Secondary)
* Order API should maintain a p95 response time of < 1 second under designated user load.

## 7. Customer End-to-End Journey
The platform operates as a modern Scheduled Pickup & Storefront Management system:
1. **Browse:** Customer checks flavor availability in real-time.
2. **Select Slot & Items:** Customer selects gelato flavors and chooses an available **Pickup Time Slot** (e.g., 14:00 - 14:15).
3. **Pay:** Completes payment immediately via Stripe.
4. **Receive Slot Queue:** Receives an Order Confirmation with a **Queue Number specifically assigned within their chosen time slot**.
5. **Arrival & Collection:** Customer arrives at the store during the designated time slot, waits for their queue number to be called/notified, and collects their Gelato via QR Code.
