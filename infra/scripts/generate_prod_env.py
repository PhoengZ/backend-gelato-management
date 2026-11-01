#!/usr/bin/env python3
"""Generate production secrets for the single-host Compose deployment.

Writes two git-ignored files:

  infra/.env.prod                      random credentials, tokens and site settings
  infra/rabbitmq/definitions.prod.json the committed broker topology with new user passwords

Usage:
  python3 infra/scripts/generate_prod_env.py --domain gelato.duckdns.org --email you@example.com

Stripe keys are left as placeholders; paste the test-mode values into infra/.env.prod.
Existing files are never overwritten unless --force is given, because replacing them
changes passwords that the database and broker volumes were initialised with.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import secrets
import sys
from pathlib import Path

INFRA = Path(__file__).resolve().parent.parent
ENV_PATH = INFRA / ".env.prod"
DEFS_SOURCE = INFRA / "rabbitmq" / "definitions.json"
DEFS_PATH = INFRA / "rabbitmq" / "definitions.prod.json"

# RabbitMQ users from definitions.json and the env variable that carries each password.
RABBITMQ_USERS = {
    "admin": "RABBITMQ_DEFAULT_PASS",
    "analytics_service": "ANALYTICS_RABBITMQ_PASSWORD",
    "inventory_service": "INVENTORY_RABBITMQ_PASSWORD",
    "fulfillment_service": "FULFILLMENT_RABBITMQ_PASSWORD",
    "notification_service": "NOTIFICATION_RABBITMQ_PASSWORD",
    "order_service": "ORDER_RABBITMQ_PASSWORD",
}


def token(nbytes: int = 32) -> str:
    """URL-safe secret; hex keeps it valid inside connection URLs without escaping."""
    return secrets.token_hex(nbytes)


def rabbitmq_hash(password: str) -> str:
    """rabbit_password_hashing_sha256: base64(salt + sha256(salt + password))."""
    salt = os.urandom(4)
    return base64.b64encode(salt + hashlib.sha256(salt + password.encode()).digest()).decode()


def build_env(domain: str, email: str) -> dict[str, str]:
    env: dict[str, str] = {
        "SITE_DOMAIN": domain,
        "ACME_EMAIL": email,
        "RABBITMQ_DEFAULT_USER": "admin",
        "AUTH_POSTGRES_DB": "gelatoflow_auth",
        "AUTH_POSTGRES_USER": "auth_service",
        "AUTH_POSTGRES_PASSWORD": token(),
        "INVENTORY_POSTGRES_DB": "gelatoflow_inventory",
        "INVENTORY_POSTGRES_USER": "inventory_service",
        "INVENTORY_POSTGRES_PASSWORD": token(),
        "FULFILLMENT_POSTGRES_DB": "gelatoflow_fulfillment",
        "FULFILLMENT_POSTGRES_USER": "fulfillment_service",
        "FULFILLMENT_POSTGRES_PASSWORD": token(),
        "ORDER_POSTGRES_DB": "gelatoflow_order",
        "ORDER_POSTGRES_USER": "order_service",
        "ORDER_POSTGRES_PASSWORD": token(),
        "CATALOG_REDIS_PASSWORD": token(),
        "MONGO_INITDB_ROOT_USERNAME": "root",
        "MONGO_INITDB_ROOT_PASSWORD": token(),
        "MONGO_INITDB_DATABASE": "analytics",
        "JWT_SECRET": token(48),
        "JWT_ISSUER": "gelatoflow-auth",
        "JWT_AUDIENCE": "gelatoflow-api",
        "ORDER_SERVICE_TOKEN": token(),
        "PAYMENT_SERVICE_TOKEN": token(),
        "PAYMENT_REQUEST_TOKEN": token(),
        "STRIPE_SECRET_KEY": "sk_test_replace_me",
        "STRIPE_WEBHOOK_SECRET": "whsec_replace_me",
        "STRIPE_LIVE_MODE": "false",
    }
    for var in RABBITMQ_USERS.values():
        env[var] = token()
    broker = "rabbitmq:5672/gelato_vhost"
    env["FULFILLMENT_RABBITMQ_URL"] = (
        f"amqp://fulfillment_service:{env['FULFILLMENT_RABBITMQ_PASSWORD']}@{broker}"
    )
    env["NOTIFICATION_RABBITMQ_URL"] = (
        f"amqp://notification_service:{env['NOTIFICATION_RABBITMQ_PASSWORD']}@{broker}"
    )
    return env


def build_definitions(env: dict[str, str]) -> dict:
    definitions = json.loads(DEFS_SOURCE.read_text())
    names = {user["name"] for user in definitions["users"]}
    if names != set(RABBITMQ_USERS):
        sys.exit(
            f"RabbitMQ users changed in {DEFS_SOURCE.name}: {sorted(names)}. "
            "Update RABBITMQ_USERS in this script before generating production secrets."
        )
    for user in definitions["users"]:
        user["password_hash"] = rabbitmq_hash(env[RABBITMQ_USERS[user["name"]]])
        user["hashing_algorithm"] = "rabbit_password_hashing_sha256"
    return definitions


def write_private(path: Path, content: str) -> None:
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as handle:
        handle.write(content)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--domain", required=True, help="public host name, e.g. gelato.duckdns.org")
    parser.add_argument("--email", required=True, help="contact address for Let's Encrypt")
    parser.add_argument("--force", action="store_true", help="overwrite existing generated files")
    args = parser.parse_args()

    domain = args.domain.strip().removeprefix("https://").removeprefix("http://").rstrip("/")
    existing = [p for p in (ENV_PATH, DEFS_PATH) if p.exists()]
    if existing and not args.force:
        sys.exit(f"Refusing to overwrite {', '.join(str(p) for p in existing)} (use --force).")

    env = build_env(domain, args.email)
    definitions = build_definitions(env)

    lines = [
        "# Generated by infra/scripts/generate_prod_env.py. Do not commit.",
        "# Replace the Stripe placeholders with test-mode keys before starting payment-service.",
    ]
    lines += [f"{key}={value}" for key, value in env.items()]
    write_private(ENV_PATH, "\n".join(lines) + "\n")
    write_private(DEFS_PATH, json.dumps(definitions, indent=2) + "\n")

    print(f"Wrote {ENV_PATH}")
    print(f"Wrote {DEFS_PATH}")
    print("Next: set STRIPE_SECRET_KEY and STRIPE_WEBHOOK_SECRET in infra/.env.prod.")


if __name__ == "__main__":
    main()
