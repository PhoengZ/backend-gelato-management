"""
Stripe Webhook Simulator for GelatoFlow Integration Testing.
Generates cryptographically signed Stripe payloads (HMAC-SHA256) matching the
payment-service webhook verification specification.
"""

import hashlib
import hmac
import json
import os
import time
from typing import Any, Dict, Optional, Tuple

import requests

DEFAULT_WEBHOOK_SECRET = os.getenv("STRIPE_WEBHOOK_SECRET", "whsec_test_secret_for_integration_testing_12345")


def sign_stripe_payload(payload_bytes: bytes, secret: str = DEFAULT_WEBHOOK_SECRET, timestamp: Optional[int] = None) -> str:
    """
    Computes standard Stripe-Signature header format: 't=<ts>,v1=<hex_signature>'.
    Signature = HMAC-SHA256(secret, f"{timestamp}.{payload}")
    """
    ts = timestamp if timestamp is not None else int(time.time())
    signed_payload = f"{ts}.".encode("utf-8") + payload_bytes
    computed_signature = hmac.new(secret.encode("utf-8"), signed_payload, hashlib.sha256).hexdigest()
    return f"t={ts},v1={computed_signature}"


def build_checkout_session_completed_payload(
    order_id: str,
    amount_minor: int,
    currency: str = "THB",
    session_id: Optional[str] = None,
    payment_intent_id: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Constructs a Stripe 'checkout.session.completed' event payload recognized by Payment Service.
    """
    sid = session_id or f"cs_test_{order_id.replace('-', '')[:16]}"
    pi_id = payment_intent_id or f"pi_{order_id.replace('-', '')[:16]}"
    now = int(time.time())

    return {
        "id": f"evt_{order_id.replace('-', '')[:16]}",
        "object": "event",
        "api_version": "2023-10-16",
        "created": now,
        "type": "checkout.session.completed",
        "livemode": False,
        "data": {
            "object": {
                "id": sid,
                "object": "checkout.session",
                "mode": "payment",
                "status": "complete",
                "payment_status": "paid",
                "amount_total": amount_minor,
                "currency": currency.lower(),
                "client_reference_id": order_id,
                "metadata": {
                    "order_id": order_id,
                    "amount_minor": str(amount_minor),
                    "currency": currency.upper(),
                },
                "payment_intent": pi_id,
            }
        },
    }


def build_payment_intent_succeeded_payload(
    order_id: str,
    amount_minor: int,
    currency: str = "THB",
    payment_intent_id: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Constructs a Stripe 'payment_intent.succeeded' event payload.
    """
    pi_id = payment_intent_id or f"pi_{order_id.replace('-', '')[:16]}"
    now = int(time.time())

    return {
        "id": f"evt_pi_{order_id.replace('-', '')[:16]}",
        "object": "event",
        "created": now,
        "type": "payment_intent.succeeded",
        "livemode": False,
        "data": {
            "object": {
                "id": pi_id,
                "object": "payment_intent",
                "status": "succeeded",
                "amount": amount_minor,
                "amount_received": amount_minor,
                "currency": currency.lower(),
                "metadata": {
                    "order_id": order_id,
                },
            }
        },
    }


def post_simulated_webhook(
    base_url: str,
    payload_dict: Dict[str, Any],
    secret: str = DEFAULT_WEBHOOK_SECRET,
    timeout: float = 5.0,
) -> requests.Response:
    """
    Sends signed webhook payload directly to API Gateway /api/v1/payments/webhook.
    """
    payload_bytes = json.dumps(payload_dict, separators=(",", ":")).encode("utf-8")
    signature_header = sign_stripe_payload(payload_bytes, secret=secret)

    url = f"{base_url.rstrip('/')}/api/v1/payments/webhook"
    headers = {
        "Content-Type": "application/json",
        "Stripe-Signature": signature_header,
    }

    return requests.post(url, data=payload_bytes, headers=headers, timeout=timeout)
