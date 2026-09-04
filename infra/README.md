# Infrastructure

This directory defines the shared development infrastructure for GelatoFlow.
Operational data remains isolated by service even when the containers share the
same Docker network.

## Services

| Container | Owner | Internal port | Development host port |
| --- | --- | ---: | ---: |
| `rabbitmq` | Shared messaging | 5672, 15672 | 5672, 15672 |
| `analytics-mongodb` | Analytics Service | 27017 | 27017 |
| `auth-postgres` | Auth Service | 5432 | 5433 |
| `inventory-postgres` | Batch Inventory Service | 5432 | 5434 |
| `catalog-redis` | Catalog Service | 6379 | 6380 |

The default compose file references pre-built images for application services.
Auth, Catalog, and Batch Inventory containers will be added only after those
services have Dockerfiles and published images.

## Local setup

Run commands from `infra/`:

```bash
cp .env.example .env
docker compose \
  --env-file .env \
  -f docker/compose.yml \
  -f docker/compose.dev.yml \
  up -d rabbitmq analytics-mongodb auth-postgres inventory-postgres catalog-redis
```

Inspect health and resolved configuration:

```bash
docker compose \
  --env-file .env \
  -f docker/compose.yml \
  -f docker/compose.dev.yml \
  ps

docker compose \
  --env-file .env \
  -f docker/compose.yml \
  -f docker/compose.dev.yml \
  config
```

`.env` is ignored by Git. Values in `.env.example` are development-only and must
be replaced outside local development.

## Production deployment

In production, deploy using only the base `docker/compose.yml` to ensure data store ports remain isolated within `gelato_network` and are not exposed to the public internet:

1. Create a production `.env` from `.env.example` on the host server and restrict file permissions:
   ```bash
   cp .env.example .env
   chmod 600 .env
   ```
2. Start infrastructure services without development port mappings:
   ```bash
   docker compose \
     --env-file .env \
     -f docker/compose.yml \
     up -d rabbitmq analytics-mongodb auth-postgres inventory-postgres catalog-redis
   ```
   Or specify an external secret store path:
   ```bash
   docker compose \
     --env-file /etc/secrets/gelatoflow.env \
     -f docker/compose.yml \
     up -d
   ```

Environment variables are interpolated by Docker Compose from `--env-file` at runtime. Container definitions do not hardcode secrets or bind unisolated environment files, ensuring services receive only their designated credentials.

## Service connection targets

Containers on `gelato_network` connect with the container names and internal
ports:

```text
Auth PostgreSQL:      auth-postgres:5432
Inventory PostgreSQL: inventory-postgres:5432
Catalog Redis:        catalog-redis:6379
RabbitMQ:             rabbitmq:5672/gelato_vhost
Analytics MongoDB:    analytics-mongodb:27017
```

Host-based service processes use the development ports from
`docker/compose.dev.yml` instead.

## RabbitMQ topology

RabbitMQ virtual hosts are isolation boundaries. A publisher and consumer must
use the same vhost to exchange messages. GelatoFlow therefore uses the shared
`gelato_vhost` while retaining one least-privilege user per service.

Current topology:

| User | Purpose | Allowed resources |
| --- | --- | --- |
| `inventory_service` | Publish Inventory events | `inventory` exchange |
| `analytics_service` | Consume Order and Inventory events | `order`, `inventory`, `analytics_queue` |

Analytics Service declares the topic exchanges and its queue at startup. Batch
Inventory Service publishes `inventory.waste` to the `inventory` exchange. Both
services must use a URL ending in `/gelato_vhost`.

To add a service user:

1. Generate a development password hash with
   `python3 dev/utils/generate-password.py <password>` from the repository root.
2. Add the user and least-privilege permissions to `rabbitmq/definitions.json`.
3. Add the matching untracked credential to the service `.env`.
4. Never commit production credentials or service `.env` files.

`rabbitmq/definitions_example.json` demonstrates a publisher and consumer sharing
one vhost.

## Adding an application service

The default compose file uses `image:` rather than local `build:` entries. After
a service implementation is ready:

1. Build and test the service image.
2. Publish a versioned image to the team's registry.
3. Add the image, service `.env`, health check, dependencies, and network entry to
   `docker/compose.yml`.
4. Add only development host ports to `docker/compose.dev.yml`.

Local iteration may run the application process on the host while its database
and RabbitMQ run in Docker.
