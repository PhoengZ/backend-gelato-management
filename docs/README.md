# GelatoFlow documentation

Start with the current [architecture overview](overview-architecture.md), then
the [system and Order diagrams](architecture-diagram.md) and
[checkout, retry, cancellation, expiry and state sequences](sequence-diagram.md).

The selected checkout is Frontend -> Gateway -> Order -> Payment -> Stripe.
Order's client and session binding, along with Payment's Checkout Session
server and webhook adapter, are implemented; durable reconciliation/refunds
and deployed verification remain pending. Current docs distinguish that target from
the runnable local service interfaces and the legacy frontend demo.

| Reference | Purpose |
| --- | --- |
| [Checkout boundary](architecture/order-checkout.md) | Totals, session binding, retry, cancellation and Payment migration |
| [Inventory reservations](architecture/inventory-reservation.md) | FEFO, balances, release, expiry and Order coordination |
| [Data ownership](architecture/service-data-ownership.md) | Service-owned records and storage boundaries |
| [Auth and JWT](architecture/auth-and-jwt.md) | Gateway identity, ownership checks and machine credentials |
| [Events](event-schema-spec.md) | Paid-sale/cancellation semantics, canonical envelopes and integration gaps |
| [Frontend demo API migration](API_SPEC.md) | Current checkout examples alongside explicitly labeled legacy demo shapes |
| [ADR-006](architecture/adr-006-catalog-rest-and-grpc.md) | Historical Catalog transport proposal |
| [ADR-007](architecture/adr-007-inventory-transactions-and-callers.md) | Historical Inventory transaction/caller proposal |

Machine-readable [contracts](../contracts/README.md) are authoritative. A diagram,
proposal or contract is not proof that a service is implemented or deployed.
The [Order README](../order-service/README.md) documents local configuration and
run commands; [Inventory](../batch-inventory-service/README.md) and
[Catalog](../catalog-service/README.md) describe their dependencies and setup.
