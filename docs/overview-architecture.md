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
| **Payment Service** | Payment processing integration. | *External/Placeholder* |

## 3. Communication Patterns

### A. REST API (External / Client-to-System)
Used for communication between the Front-end (Customer Web, Staff/Manager Dashboards) and the backend services via the API Gateway.
* **Examples:**
  * `GET /flavors`
  * `GET /timeslots` *(Fetch available pickup time slots)*
  * `POST /orders` *(Includes the selected time slot in the payload)*
  * `GET /orders/{id}`
  * `PATCH /fulfillments/{id}/status`
  * `POST /batches`

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

### Ordering Flow & Queue Reservation (Happy Path)
1. **Customer** checks real-time stock and available time slots (`GET /timeslots`), then sends `POST /orders` (including the selected pickup time slot) via **API Gateway**.
2. **Order Service** initiates order creation and binds it to the requested time slot.
3. **Order Service** calls **Batch Inventory Service** via `gRPC (ReservePortions)`.
4. **Batch Inventory Service** confirms reservation (if stock is sufficient).
5. **Order Service** saves the confirmed order.
6. **Order Service** publishes `OrderPlaced` event to **RabbitMQ**.
7. **Fulfillment Service** consumes the event, allocates the queue number for that specific time slot, and adds the order to the preparation queue.
8. **Notification Service** consumes the event and sends an order confirmation to the Customer.
9. **Customer** receives an Order Number and a **Queue Number specifically allocated for their chosen time slot**.

*(If stock is insufficient at step 4, Batch Inventory returns an Out of Stock error, and Order Service aborts, notifying the customer).*

### Fulfillment Flow
1. Staff prepares the order according to the time-slot queue.
2. Staff changes order status to "Ready".
3. **Fulfillment Service** publishes `OrderReady` event to **RabbitMQ**.
4. **Notification Service** consumes the event and alerts the customer for pickup.

## 5. Data Integrity & Overselling Prevention
Preventing overselling is the most critical business requirement.
* **Single Authority:** `Batch Inventory Service` is the sole decision-maker for stock.
* **Mechanisms Required:**
  * **Atomic Database Transactions**
  * **Row Locking or Optimistic Locking**
  * **Idempotency Keys** (to prevent duplicate order processing)
  * **Transactional Outbox Pattern** (to guarantee event publishing after DB commits)
* **Constraints:** `available_portions` must NEVER be negative. Reservations MUST be released if an order is cancelled.
* **Inventory Formula:** 
  `Available = Produced - Reserved - Sold - Waste`

## 6. Quality Attributes

### A. Consistency (Primary)
* Strict consistency is required to prevent overselling.
* **Test Case:** If 1 portion of Pistachio remains and 20 concurrent requests arrive, exactly 1 order must succeed, 19 must fail, stock must not drop below 0, and no duplicate reservations should occur.

### B. Performance (Secondary)
* Order API should maintain a p95 response time of < 1 second under designated user load.

## 7. Customer End-to-End Journey
The platform acts as a unified system (Pre-booking + Delivery Pickup + POS).
1. Customer checks real-time availability via the web app.
2. **Customer selects items and a specific pickup time slot for storefront collection.**
3. Completes payment immediately.
4. **Receives a Queue Number explicitly bound to that selected time slot.**
5. Travels to the physical store at the designated time.
6. Waits for their queue number within their slot and picks up the Gelato via QR Code/Order reference.
