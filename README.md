# GelatoFlow Backend

Backend services, contracts, and shared development infrastructure for the
GelatoFlow preorder, queue, and batch inventory system.

## Current repository status

| Area | Status |
| --- | --- |
| Analytics Service | Implemented prototype |
| Auth Service | Contract and PostgreSQL infrastructure defined |
| Catalog Service | Contract and Redis infrastructure defined |
| Batch Inventory Service | REST, gRPC, event contracts and PostgreSQL infrastructure defined |
| Other application services | Added by their owners in later pull requests |

A contract or infrastructure definition is not an implementation. Service source,
migrations, tests, images, and runtime verification are added in separate pull
requests.

## Repository layout

```text
analytics-service/   Existing analytics implementation
contracts/           Versioned REST, gRPC, and event interfaces
docs/architecture/   Service boundaries and architecture decisions
infra/               Docker Compose and RabbitMQ definitions
.github/workflows/   Service and contract validation
```

## Architecture boundaries

- Auth owns users, credential hashes, roles, and access-token issuance.
- Catalog owns flavor metadata, price, recipe, and allergens.
- Batch Inventory owns batches, availability, reservations, movements, and waste.
- Order calls Inventory through gRPC and never reads Inventory tables.
- Analytics receives asynchronous events and does not join operational
  transactions.
- API Gateway or a frontend adapter may compose Catalog metadata with Inventory
  availability for the demo UI.

See `docs/architecture/` for the detailed decisions and `contracts/README.md` for
interface ownership.

## Validate contracts

OpenAPI:

```bash
npx --yes @redocly/cli@2.49.0 lint
```

Protobuf, with Buf installed:

```bash
buf lint contracts
buf build contracts
```

Inventory waste event example:

```bash
python3 -m pip install jsonschema==4.26.0
python3 contracts/scripts/validate_event_examples.py
```

## Start development infrastructure

```bash
cd infra
cp .env.example .env
docker compose \
  --env-file .env \
  -f docker/compose.yml \
  -f docker/compose.dev.yml \
  up -d rabbitmq analytics-mongodb auth-postgres inventory-postgres catalog-redis
```

See `infra/README.md` for connection targets, local ports, RabbitMQ permissions,
and the application-service image workflow.
