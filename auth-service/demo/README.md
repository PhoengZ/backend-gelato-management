# Auth preparation for the Round 2 demo

This setup runs real Auth and its own PostgreSQL database. It prepares Customer,
Staff and Manager accounts for the three business use cases. It does not start
or change Gateway, Catalog, Inventory, Order, Payment, Fulfillment, Notification
or Analytics. Authentication readiness is separate from a complete Round 2 demo.

The primary `auth` branch starts at main `7c46029` and imports only the Auth service,
Auth OpenAPI and Auth CI from the earlier work. The original `auth-hardening`
branch remains available for reference. Its Gateway/Analytics/Fulfillment patches
are not included. Teammates' implementations already present on main are retained.

## Start and present

Requirements: Docker Desktop with Compose, Go matching `auth-service/go.mod`,
and Python 3.9 or newer. On macOS open:

1. `START.command` — build/start Auth, apply migrations and prepare all three roles.
2. `DEMO.command` — refresh tokens, verify identities and show rejected requests.
3. `WEB.command` — run the existing frontend on port 3101 using real backend Auth.
4. `STOP.command` — stop this Auth stack while retaining its database and fixtures.

`WEB.command` requires the separate frontend repository and its installed Node
dependencies. It uses runtime configuration; no frontend source is modified. A
second Next dev server cannot share a running development instance's lock. Stop
the foreground process you started before opening another one.

Equivalent commands from the backend repository:

```sh
python3 auth-service/demo/demo.py up
python3 auth-service/demo/demo.py seed
python3 auth-service/demo/demo.py demo --pause
python3 auth-service/demo/demo.py web
```

| Endpoint | Address |
|---|---|
| Auth REST | `http://127.0.0.1:17011` |
| PostgreSQL | `127.0.0.1:17433`, database/user `auth_demo` |
| Frontend Auth UI | `http://127.0.0.1:3101/login` |

Compose project `gelatoflow-auth-demo` owns only these services and its
`auth_data` volume. All published ports bind to loopback. It is independent of
the retained `gelatoflow-owned-demo` Catalog/Batch stack and shared infrastructure.

## Accounts and token handling

`cmd/demo` signs up all new fixtures through the real HTTP endpoint without a
role field. Every account initially becomes Customer. Before trusted local
provisioning it verifies the account through login, `/me` and the Auth database.
The exact generated UUID and fixture email are required for the Staff/Manager
update; the command refuses databases other than `auth_demo`.

After provisioning it logs in again and verifies all three identities. Repeating
the command reuses the accounts and refreshes the Auth-issued tokens. It never
signs a user token locally or adds a public role-management endpoint.

Private files are generated under ignored `demo/.local/` with directory mode
0700 and file mode 0600:

- `config.json`, `compose.env`: runtime database and JWT configuration.
- `credentials.json`: Customer/Staff/Manager emails, passwords, IDs and current
  access tokens. Open this locally when entering a login form; do not include it
  in a recording or review archive.
- `frontend.env`: real backend Auth settings; it contains no signing secret.
- `server-jwt.env`: server-side JWT settings for integration with the team.
- `postman.environment.json`: private account and token variables for Postman.
- `verification.json`, `boundary-verification.json`: outcomes without credentials.

The visible demo shows real login and `/me` for all roles, incorrect password
401, anonymous `/me` 401 and public-signup role injection 400. User JWTs expire
after 15 minutes. `seed`, `demo` or `verify` performs real login again.

For Postman, import `postman.collection.json` and the private environment generated
by `seed`, then select that environment. The nine requests verify all three roles
and the three failure cases. Login requests refresh their token variables. Keep
the environment local; the tracked collection contains placeholders only.

```sh
python3 auth-service/demo/demo.py verify
python3 auth-service/demo/demo.py status
python3 auth-service/demo/demo.py logs
python3 auth-service/demo/demo.py down
```

`down` preserves data and local configuration. There is no automatic reset or
volume deletion command. Preserve database credentials with their retained volume.

## Connect to Catalog, Batch and the team's services

Every server that verifies these user JWTs needs the same `JWT_SECRET`,
`JWT_ISSUER=gelatoflow-auth` and `JWT_AUDIENCE=gelatoflow-api`. On the **first**
start, reuse an existing local server environment instead of generating a
different signing key:

```sh
python3 auth-service/demo/demo.py up --jwt-env-file /absolute/path/to/server.env
```

The import reads only JWT settings as literal values; it does not execute a shell
file. Existing configuration is retained on later starts. A conflicting import
is rejected rather than silently rotating keys. Alternatively configure the
team's server verifiers from the private `server-jwt.env` generated here.

The Catalog/Batch integration checked on this machine uses the existing owned
demo's JWT environment on the first start. Their source, containers and stored
demo records are not replaced by this Auth setup. Point frontend/Gateway `/me`
requests to **this** Auth origin: two independent Auth databases do not share
users merely because their JWT configuration matches.

Frontend configuration uses server-side environment variables:

```dotenv
AUTH_MODE=backend
AUTH_SERVICE_URL=http://127.0.0.1:17011
JWT_ISSUER=gelatoflow-auth
JWT_AUDIENCE=gelatoflow-api
NEXT_PUBLIC_API_BASE_URL=
```

Frontend backend mode uses a HttpOnly session cookie and real Go Auth identity
verification. Browser logout clears that cookie; it does not revoke a copied
stateless bearer token. The frontend signing secret is unnecessary in backend mode.

Gateway's `AUTH_SERVICE_URL` uses the same origin on a native process. For a
Docker Desktop container on this Mac use `http://host.docker.internal:17011`.
An Order backend needs the separate `ORDER_SERVICE_TOKEN` for Inventory gRPC;
this Auth setup does not create or replace that machine credential.

## Scope of the Round 2 handoff

Customer is used for preorder/payment, Staff for kitchen/pickup and Manager for
production/waste. The existing public Auth contract remains signup/login/me.

This setup proves authentication, local account persistence and token
compatibility. The project still needs actual Order/Payment/Fulfillment/
Notification/Analytics integration, broker consume effects, business UI adapters
and a clip covering all three use cases. Existing frontend business mock routes
are not changed here, and no teammate role-guard patches are added.

## Local verification — 10 October 2026

- Auth race suite with real isolated PostgreSQL: 50 top-level tests and 155
  passing outcomes; one cross-peer test skipped because this host has no
  loopback alias at `127.0.0.2`.
- Real three-role signup/provision/login/me, rejected password/token/role
  injection, repeated verification and stop/start persistence passed.
- Actual browser login, role navigation and logout passed for all three roles;
  the unchanged frontend's 114 tests passed. Node 26 test execution used
  `NODE_OPTIONS=--no-experimental-webstorage` for JSDOM compatibility.
- New Auth-issued tokens passed the existing Catalog REST/gRPC demo and
  Inventory's Customer403/Staff200/Manager200 protected-list checks.
- The unchanged main Gateway forwarded real Auth login/me and user JWTs to
  Catalog/Inventory correctly; no teammate source patch was needed for those
  checks. This does not verify Order identity forwarding or payment integration.
- All nine Postman request templates passed HTTP/role assertions. The Postman
  application itself was not automated.
- Docker build, Auth OpenAPI lint, formatting, vet, private-file permissions and
  public-evidence secret scan passed. Source diff against main is restricted to
  Auth service, Auth OpenAPI and Auth CI.
