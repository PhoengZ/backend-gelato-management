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
| `fulfillment-postgres` | Fulfillment Service | 5432 | 5435 |
| `fulfillment-service` | Fulfillment Service | 8080 | 8081 |
| `notification-service` | Notification Service (stateless, no database) | 8080 | 8082 |
| `order-postgres` | Order Service | 5432 | 5436 |
| `catalog-redis` | Catalog Service | 6379 | 6380 |

The default compose file builds application services from their Dockerfiles,
except Analytics, which uses the published `phoeng/analytics-service:1.1.0` image.

## Local setup

Run commands from `infra/`:

```bash
cp .env.example .env
docker compose \
  --env-file .env \
  -f docker/compose.yml \
  -f docker/compose.dev.yml \
  up -d rabbitmq analytics-mongodb auth-postgres inventory-postgres order-postgres catalog-redis
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
     up -d rabbitmq analytics-mongodb auth-postgres inventory-postgres order-postgres catalog-redis
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
Order PostgreSQL:     order-postgres:5432
Catalog Redis:        catalog-redis:6379
RabbitMQ:             rabbitmq:5672/gelato_vhost
Analytics MongoDB:    analytics-mongodb:27017
```

Host-based service processes use the development ports from
`docker/compose.dev.yml` instead.

## Service discovery and health checks

Docker DNS resolves service names on `gelato_network`. Gateway upstream URLs
and internal gRPC targets use those names and container ports, never container
IP addresses. For example, Gateway uses `http://catalog-service:3002`, and Order
uses `batch-inventory-service:50051`. Analytics explicitly listens on port 8080
to match its Gateway URL, development mapping and health probe.

All application health checks are configured in `docker/compose.yml`. Probes
run inside each container against loopback and do not require published host
ports or JWTs. Go application probes use Alpine's BusyBox `wget`, a 3-second
request timeout, a 5-second Docker timeout, a 15-second interval and three
consecutive failures. Auth and Inventory allow 60 seconds for startup/migrations;
other Go application probes allow 30 seconds. Payment retains its existing settings
(30-second interval, 5-second timeout, three failures, 45-second startup period).

| Service | Probe | What success establishes |
| --- | --- | --- |
| API Gateway | `http://127.0.0.1:8080/health` | Gateway HTTP liveness |
| Auth | `http://127.0.0.1:3001/health` | Auth HTTP response and owned PostgreSQL connectivity |
| Catalog | `http://127.0.0.1:3002/health` | Catalog HTTP liveness |
| Batch Inventory | `http://127.0.0.1:3004/ready` | Inventory HTTP response and owned database/schema readiness |
| Order | `http://127.0.0.1:3003/health` | Order HTTP liveness |
| Analytics | `http://127.0.0.1:8080/health` | Analytics HTTP liveness |
| Fulfillment | `http://127.0.0.1:8080/health` | Owned PostgreSQL connectivity and broker connection when configured |
| Notification | `http://127.0.0.1:8080/health` | HTTP response and broker connection when configured |
| Payment | `http://127.0.0.1:3007/actuator/health/liveness` | Payment HTTP/process liveness |

Gateway waits for all eight downstream services with `condition:
service_healthy`. Order waits for Catalog, Inventory and Payment health checks;
Inventory waits for Catalog. Existing database and broker health dependencies
remain in place. Keep Payment independent of Order startup to avoid a dependency
cycle through its reverse result callbacks.

These gates apply at startup. HTTP liveness does not prove that gRPC, all
dependencies or checkout are ready. An unhealthy status does not automatically
restart a running container or remove its DNS record/Gateway route;
`restart: unless-stopped` handles process exits. This is a single-host Compose
setup without health-aware load balancing or automatic failover.

Inspect health after starting the stack with your private environment file:

```bash
docker compose --env-file .env \
  -f docker/compose.yml -f docker/compose.dev.yml ps -a
docker inspect --format '{{json .State.Health}}' api-gateway
docker inspect --format '{{json .State.Health}}' batch-inventory-service
docker inspect --format '{{json .State.Health}}' analytics-service
```

For the published Analytics image, verify that `wget` and `/health` are present
in the deployed version. Local source alone does not certify a registry image.
Configuration validation does not establish runtime health; verify the actual
images and startup sequence before claiming an end-to-end pass.

## RabbitMQ topology

RabbitMQ virtual hosts are isolation boundaries. A publisher and consumer must
use the same vhost to exchange messages. GelatoFlow therefore uses the shared
`gelato_vhost` while retaining one least-privilege user per service.

Current topology:

| User | Purpose | Allowed resources |
| --- | --- | --- |
| `inventory_service` | Publish Inventory events | `inventory` exchange |
| `order_service` | Publish Order events | `order` exchange |
| `analytics_service` | Consume Order and Inventory events | `order`, `inventory`, `analytics_queue` |
| `fulfillment_service` | Consume Order events, publish Fulfillment events | `order`, `fulfillment`, `fulfillment_queue`, `fulfillment_queue_retry`, `fulfillment.dlx`, `fulfillment_queue_dlq` |
| `notification_service` | Consume Order, Fulfillment and Inventory events | read `order`, `fulfillment`, `inventory`; own `notification_queue` |

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

After a service implementation is ready:

1. Build and test the service image.
2. Choose a local `build:` entry or publish a versioned image to the team's registry.
3. Add the build/image, service environment, health check, dependencies, and
   network entry to `docker/compose.yml`.
4. Add only development host ports to `docker/compose.dev.yml`.

Local iteration may run the application process on the host while its database
and RabbitMQ run in Docker.

## Payment local build

Payment has no database or broker dependency. `docker/compose.payment.yml` is an
optional local build override, used with the base/development files. It requires
Stripe, Auth and Payment-to-Order secrets supplied by the shell or a private env
file; no published image is assumed. See `../payment-service/README.md` for exact
commands, TLS configuration and the required Order gRPC implementation.
