# GelatoFlow Contracts

This directory contains the versioned interfaces shared between GelatoFlow
services. Contract changes are reviewed before service or frontend implementation
changes.

## Contract types

- `openapi/`: REST APIs exposed through API Gateway.
- `proto/`: Internal synchronous service-to-service APIs.
- `events/`: Asynchronous RabbitMQ message schemas.
- `examples/`: Review and validation fixtures.

## Ownership

| Contract | Producer or owner | Primary consumers |
| --- | --- | --- |
| Auth REST API | Auth Service | API Gateway, frontend |
| Catalog REST API | Catalog Service | API Gateway, frontend |
| Inventory REST API | Batch Inventory Service | API Gateway, staff and manager UI |
| Inventory gRPC API | Batch Inventory Service | Order Service |
| `inventory.waste` event | Batch Inventory Service | Analytics Service, Notification Service |

## Inventory gRPC behavior

The gRPC service uses standard status codes:

| Code | Meaning |
| --- | --- |
| `INVALID_ARGUMENT` | Missing IDs, duplicate flavors, or non-positive portions |
| `NOT_FOUND` | Reservation does not exist |
| `FAILED_PRECONDITION` | Insufficient stock or invalid reservation state |
| `ALREADY_EXISTS` | An idempotency key is reused with a different request |
| `UNAVAILABLE` | Inventory storage is temporarily unavailable |

Repeating an operation with the same idempotency key and the same request returns
the original successful response.

## Event compatibility

`inventory.waste` is published to the `inventory` topic exchange with routing key
`inventory.waste`. Version 1 keeps `date`, `flavor_id`, `portions`, `batch_id`,
`reason`, and `cost_lost` at the top level for compatibility with the existing
Analytics Service. Metadata fields are additive.

Consumers must ignore unknown fields so compatible metadata can be added without
breaking existing readers. A breaking field or semantic change requires a new
schema version and routing-key migration plan.

## Frontend integration

The frontend demo is not required to adopt these contracts in the first pull
request. Later integration should compose Catalog metadata with Inventory
availability at API Gateway or a frontend-facing adapter.
