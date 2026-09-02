sequenceDiagram
    autonumber
    actor C as Customer (Web)
    participant AG as API Gateway
    participant CS as Catalog Service
    participant OS as Order Service
    participant BIS as Batch Inventory Service
    participant PS as Payment Service
    participant STR as Stripe (External)
    participant RMQ as RabbitMQ
    participant FS as Fulfillment Service
    participant NS as Notification Service

    rect rgb(30, 30, 30)
    note right of C: 1. Check Real-time Availability
    end
    C->>AG: GET /api/v1/catalog/flavors & timeslots
    AG->>CS: GET /flavors & /timeslots
    CS-->>AG: Return available slots & stock
    AG-->>C: Return data

    rect rgb(30, 30, 30)
    note right of C: 2. Order Creation & Stock Reservation (Synchronous)
    end
    C->>AG: POST /api/v1/orders (items, timeslot)
    AG->>OS: POST /orders
    activate OS
    OS->>BIS: gRPC: ReservePortions(flavorId, portions)
    activate BIS
    BIS-->>OS: Confirmed reservation
    deactivate BIS
    OS-->>AG: Order created (status: PENDING_PAYMENT)
    deactivate OS
    AG-->>C: Return orderId

    rect rgb(30, 30, 30)
    note right of C: 3. Payment Processing (Immediate)
    end
    C->>AG: POST /api/v1/stripe/create-payment-intent
    AG->>PS: CreatePaymentIntent(orderId, amount)
    PS->>STR: Create payment intent
    STR-->>PS: Return clientSecret
    PS-->>AG: Return clientSecret
    AG-->>C: Return clientSecret
    
    C->>STR: Complete payment using clientSecret
    STR->>PS: Webhook (payment_intent.succeeded)
    PS->>OS: Update order (status: PAID)

    rect rgb(30, 30, 30)
    note right of C: 4. Asynchronous Workflows (Queue Allocation, Notification)
    end
    OS->>RMQ: Publish OrderPlaced(orderDetails, status: PAID)
    
    par Fulfillment
        RMQ->>FS: Consume OrderPlaced event
        activate FS
        FS->>FS: Allocate queue number for timeslot
        FS->>FS: Add to preparation queue & DB
        deactivate FS
    and Notification
        RMQ->>NS: Consume OrderPlaced event
        activate NS
        NS->>C: Send Order Confirmation with Queue Number
        deactivate NS
    end

    C->>AG: Poll GET /api/v1/orders/{id}/status
    AG->>OS: GET order status
    OS-->>AG: Return status (e.g., PREPARING)
    AG-->>C: Return Queue Status