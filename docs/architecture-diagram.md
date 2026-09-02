flowchart LR
    %% Clients
    CW[Customer Web]
    SD[Staff Dashboard]
    MD[Manager Dashboard]

    %% API Gateway
    AG[API Gateway]

    %% Services
    AS[Auth Service]
    CS[Catalog Service]
    OS[Order Service]
    BIS[Batch Inventory Service]
    PS[Payment Service]
    FS[Fulfillment Service]
    NS[Notification Service]
    ANS[Analytics Service]

    %% Databases & Infrastructure
    AuthDB[(PostgreSQL Auth DB)]
    CatDB[(Redis Catalog DB)]
    OrdDB[(PostgreSQL Order DB)]
    InvDB[(PostgreSQL Inventory DB)]
    FulDB[(PostgreSQL Fulfillment DB)]
    AnaDB[(MongoDB Analytics DB)]
    RMQ(((RabbitMQ Message Broker)))
    Stripe((Stripe API))

    %% Client Connections
    CW --> AG
    SD --> AG
    MD --> AG

    %% Gateway to Services
    AG --> AS
    AG --> CS
    AG --> OS
    AG --> BIS
    AG --> PS
    AG --> FS
    AG --> ANS

    %% Service to Database Connections
    AS --> AuthDB
    CS --> CatDB
    OS --> OrdDB
    BIS --> InvDB
    FS --> FulDB
    ANS --> AnaDB

    %% Inter-service & External Connections
    OS -- gRPC: Reserve/Release Portions --> BIS
    PS -- Update Order Status --> OS 
    PS -- External API --> Stripe
    Stripe -- Webhook --> PS

    %% Message Broker Connections (Publishers)
    OS -- Publish: OrderPlaced / OrderCancelled --> RMQ
    BIS -- Publish: BatchLowStock / BatchExpiring --> RMQ
    FS -- Publish: OrderReady / PickedUp --> RMQ

    %% Message Broker Connections (Consumers)
    RMQ -- Consume --> FS
    RMQ -- Consume --> NS
    RMQ -- Consume --> ANS