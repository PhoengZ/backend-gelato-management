# Auth NFR ownership and verification

The attached Project Progress 2 PDF (section 8, page 11) defines NFR-01 through NFR-09. NFR-10 through NFR-13 are additional repository requirements in `../docs/architecture/non-functional-requirements.md`; they are not additional rows in that PDF. An isolated service test is not proof of production or the complete Order checkout.

| Requirement | Owned implementation and evidence | Remaining boundary |
| --- | --- | --- |
| 01/02 Stock consistency/concurrency | Not an Auth responsibility. | Inventory owns balances and reservations. |
| 03 Order p95 | Credential work has admission limits and request deadlines. | The Order API's agreed load and p95 require its owner and full checkout measurement. |
| 04 Notification fault isolation | Authentication does not call Notification or depend on its availability. | Confirmed-order retention is an Order integration assertion. |
| 05 Messaging reliability | Signup commits the user and USER_REGISTERED outbox row in one PostgreSQL transaction. | No event consumer runs here; publication, consumer deduplication and DLQ integration are not established by this patch. |
| 06 Security | Local JWT validation, strict roles, bounded bcrypt and opt-in HTTPS with TLS 1.2 minimum. | Gateway HTTPS URL/trust, certificates and secure PostgreSQL deployment must be coordinated before production. |
| 07 Privacy | API/migrator storage diagnostics contain fixed text and error type, never raw DSNs or database errors. Credential handlers do not log passwords/tokens; bounded audit failures are observable. | No claim is made about logs emitted by out-of-scope components. |
| 08 Auditability | PostgreSQL login/signup audit with safe User-Agent bounds and distinct operational failures. | Audit remains best effort during a DB outage; there is no public role-change endpoint in this contract. |
| 09 Ownership | Runtime reads/writes Auth PostgreSQL only; other services verify JWTs or use Auth APIs. | No cross-service private database access is introduced. |
| 10 Money | No money/card model or payment operations. | Monetary calculations belong to Catalog/Inventory/Order/Payment. |
| 11 Idempotency extension | Signup uniqueness and credential semantics are preserved. | The canonical authentication API defines no Idempotency-Key replay contract. Caching/replaying access tokens is not introduced under a generic REST-mutation statement. |
| 12/13 Deployment | No new operational dependencies or exposed storage ports. | Payment/Notification statelessness and production network isolation belong to their owners/shared deployment. |

## Optional HTTPS

Set both `HTTP_TLS_CERT_FILE` and `HTTP_TLS_KEY_FILE` to mounted PEM files. Invalid or partial configuration fails startup with bounded diagnostics; there is no fallback to plaintext after TLS is requested. With both absent, the existing local HTTP behavior remains. No route, JSON, JWT or role contract changes.

Use a certificate trusted by the caller and valid for the service hostname. Never disable certificate verification or commit private keys. The image runs as a non-root user; mounted key/certificate files must be readable by that service identity. Existing Gateway/demo clients continue using their previous local configuration until their owner enables HTTPS/trust explicitly.

## Verification

- `go test -race ./...` with isolated `TEST_DATABASE_URL`: credential/RBAC/lifecycle, audit persistence and startup privacy.
- `internal/transportsecurity/tls_test.go`: trusted HTTPS, untrusted/plaintext rejection, TLS version floor and fail-closed configuration.
- `tests/process_privacy_test.go`: actual API and migration executables with malformed/unavailable DSNs.
- The owned TLS smoke verification uses real Auth-issued Customer/Staff/Manager tokens against Catalog and Inventory. It does not exercise Gateway or Order.
