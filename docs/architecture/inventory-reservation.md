# Inventory Reservation Decision

Batch Inventory Service is the source of truth for portion availability. Its most
important quality requirement is preventing overselling when concurrent orders
request the same remaining portions.

## Portion balances

Each batch maintains non-negative balances:

- `available_portions`
- `reserved_portions`
- `sold_portions`
- `wasted_portions`

The balances must satisfy this invariant:

```text
initial_portions = available + reserved + sold + wasted
```

Expired batches are excluded from availability even if their stored available
balance is greater than zero.

## Reservation lifecycle

```text
ACTIVE --ConfirmReservation--> CONFIRMED
ACTIVE --ReleaseReservation--> RELEASED
ACTIVE --timeout-------------> EXPIRED
```

- Reserving moves portions from `available` to `reserved`.
- Confirming moves portions from `reserved` to `sold`.
- Releasing or expiring moves portions from `reserved` back to `available`, unless
  the batch has expired and the portions must be recorded as waste.

## Allocation policy

Inventory allocates portions using FEFO (first-expired, first-out):

1. Exclude inactive and expired batches.
2. Sort by `expires_at`, then by creation time.
3. Lock candidate rows inside one database transaction.
4. Allocate the requested quantity across the minimum required batches.
5. Update balances and create the reservation before committing.

Order Service requests quantities by `flavor_id`; it does not choose batch IDs.

## Concurrency and idempotency

`ReservePortions`, `ConfirmReservation`, and `ReleaseReservation` accept an
`idempotency_key`. Repeating the same operation with the same key returns the
original result rather than changing stock twice.

Reservation updates use a PostgreSQL transaction and row-level locking. The
implementation must reject the entire reservation when the full requested
quantity cannot be allocated; partial success is not allowed in version 1.

Expected contention test:

```text
Given one available portion
When 20 reservation requests run concurrently
Then exactly one request succeeds
And the final available balance is zero, never negative
```

## Integration contract

Order Service calls the following internal gRPC operations:

- `CheckAvailability`
- `ReservePortions`
- `ConfirmReservation`
- `ReleaseReservation`

Successful payment leads to confirmation. Payment failure, cancellation, or
timeout leads to release. Inventory waste is published asynchronously after the
inventory transaction commits. The service implementation should use an outbox
record so a committed waste operation cannot silently lose its event.
