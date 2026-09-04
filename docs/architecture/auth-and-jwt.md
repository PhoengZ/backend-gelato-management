# Auth and JWT Decision

Auth Service owns user identity and role assignment. API Gateway authenticates
external requests by validating the access token issued by Auth Service.

## Version 1 authentication flow

1. A user signs up or logs in through Auth Service.
2. Auth Service verifies the credentials and issues a short-lived JWT access
   token.
3. The client sends the token as `Authorization: Bearer <token>`.
4. API Gateway verifies the token before forwarding protected requests.
5. Downstream services authorize the request using the trusted user ID and role
   forwarded by the gateway.

The frontend demo may continue using its mock session cookie until integration.
The backend bearer-token contract remains canonical.

## Required claims

| Claim | Meaning |
| --- | --- |
| `sub` | User UUID |
| `role` | `CUSTOMER`, `STAFF`, or `MANAGER` |
| `iss` | Token issuer, fixed to the Auth Service |
| `aud` | Intended GelatoFlow API audience |
| `iat` | Issued-at time |
| `exp` | Expiration time |

Version 1 uses a short-lived access token. Logging out means deleting the token on
the client. Refresh tokens and server-side token revocation are deliberately
deferred until the team needs longer sessions.

## Authorization baseline

| Capability | Customer | Staff | Manager |
| --- | :---: | :---: | :---: |
| Read active flavor catalog | Yes | Yes | Yes |
| Read availability | Yes | Yes | Yes |
| Manage fulfillment queue | No | Yes | Yes |
| Manage flavors and allergens | No | No | Yes |
| Read batches and record waste | No | Yes | Yes |
| Create or adjust batches | No | No | Yes |
| View analytics | No | No | Yes |

## Security requirements

- Passwords are stored only as adaptive password hashes, never as plaintext.
- JWT signing secrets or keys are supplied through runtime configuration and are
  never committed to the repository.
- Login failures return one generic credential error to avoid account discovery.
- Role assignment during public signup is always `CUSTOMER`; privileged roles
  require an administrative operation.
- Rate limiting is applied by API Gateway, while Auth Service remains responsible
  for credential validation and token issuance.
