"""
Unit tests for test suite helpers (Api Client, JWT minting, Stripe simulator).
Runs offline without requiring running gateway containers.
"""

import base64
import json
import time
import pytest

from tests.helpers.api_client import mint_jwt, ApiClient
from tests.helpers.stripe_simulator import (
    sign_stripe_payload,
    build_checkout_session_completed_payload,
    build_payment_intent_succeeded_payload,
)


@pytest.mark.no_gateway_check
class TestHelpersUnit:
    """Offline unit verification for helper modules."""

    def test_pure_python_jwt_minting(self):
        secret = "super_secret_test_key_at_least_32_bytes_long"
        token = mint_jwt(
            user_id="11111111-2222-3333-4444-555555555555",
            role="MANAGER",
            secret=secret,
            issuer="gelatoflow-auth",
            audience="gelatoflow-api",
            exp_seconds=1800,
        )

        parts = token.split(".")
        assert len(parts) == 3, "JWT must contain header, payload, signature"

        # Decode payload
        payload_b64 = parts[1]
        padding = "=" * (-len(payload_b64) % 4)
        payload_json = base64.urlsafe_b64decode(payload_b64 + padding).decode("utf-8")
        payload = json.loads(payload_json)

        assert payload["sub"] == "11111111-2222-3333-4444-555555555555"
        assert payload["role"] == "MANAGER"
        assert payload["iss"] == "gelatoflow-auth"
        assert payload["aud"] == "gelatoflow-api"
        assert payload["exp"] > payload["iat"]

    def test_stripe_signature_format(self):
        secret = "whsec_mock_test_secret_123"
        payload = b'{"id":"evt_123"}'
        timestamp = 1700000000

        sig_header = sign_stripe_payload(payload, secret=secret, timestamp=timestamp)
        assert sig_header.startswith("t=1700000000,v1=")
        parts = dict(kv.split("=") for kv in sig_header.split(","))
        assert parts["t"] == "1700000000"
        assert len(parts["v1"]) == 64  # SHA256 hex length

    def test_stripe_payload_builders(self):
        order_id = "55555555-6666-7777-8888-999999999999"
        checkout_payload = build_checkout_session_completed_payload(
            order_id=order_id,
            amount_minor=25000,
            currency="THB",
        )
        assert checkout_payload["type"] == "checkout.session.completed"
        obj = checkout_payload["data"]["object"]
        assert obj["client_reference_id"] == order_id
        assert obj["amount_total"] == 25000
        assert obj["metadata"]["order_id"] == order_id

        pi_payload = build_payment_intent_succeeded_payload(
            order_id=order_id,
            amount_minor=25000,
            currency="THB",
        )
        assert pi_payload["type"] == "payment_intent.succeeded"
        assert pi_payload["data"]["object"]["metadata"]["order_id"] == order_id
