"""
Publishes Order CloudEvents straight to RabbitMQ through the management HTTP API.

Lets tests drive Fulfillment (and other order.* consumers) without going through
payment, which needs a real Stripe test account.
"""

import datetime
import json
import os
import uuid
from typing import Any, Dict, Optional

import requests

RABBITMQ_MGMT_URL = os.getenv("RABBITMQ_MGMT_URL", "http://localhost:15672").rstrip("/")
RABBITMQ_MGMT_USER = os.getenv("RABBITMQ_MGMT_USER", "admin")
RABBITMQ_MGMT_PASS = os.getenv("RABBITMQ_MGMT_PASS", "secure_admin_password")
RABBITMQ_VHOST = os.getenv("RABBITMQ_VHOST", "gelato_vhost")

ORDER_EXCHANGE = "order"
EVENT_TYPES = {
    "order.placed": "com.gelatoflow.order.placed.v1",
    "order.cancelled": "com.gelatoflow.order.cancelled.v1",
}


def _auth():
    return (RABBITMQ_MGMT_USER, RABBITMQ_MGMT_PASS)


def mgmt_available() -> bool:
    """True when the RabbitMQ management API answers with the configured credentials."""
    try:
        return requests.get(f"{RABBITMQ_MGMT_URL}/api/overview", auth=_auth(), timeout=2.0).status_code == 200
    except requests.RequestException:
        return False


def publish_order_event(routing_key: str, data: Dict[str, Any], event_id: Optional[str] = None) -> str:
    """Publish a structured CloudEvent to the order exchange and return its id."""
    event_id = event_id or str(uuid.uuid4())
    envelope = {
        "specversion": "1.0",
        "id": event_id,
        "source": "/gelatoflow/order-service",
        "type": EVENT_TYPES[routing_key],
        "time": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "datacontenttype": "application/json",
        "data": data,
    }
    res = requests.post(
        f"{RABBITMQ_MGMT_URL}/api/exchanges/{RABBITMQ_VHOST}/{ORDER_EXCHANGE}/publish",
        auth=_auth(),
        json={
            "properties": {"delivery_mode": 2, "content_type": "application/json"},
            "routing_key": routing_key,
            "payload": json.dumps(envelope),
            "payload_encoding": "string",
        },
        timeout=5.0,
    )
    res.raise_for_status()
    if not res.json().get("routed"):
        raise RuntimeError(f"{routing_key} was not routed to any queue; is the consumer queue declared?")
    return event_id


def order_placed_data(order_id: str, customer_id: str, pickup_at: str, flavor_id: Optional[str] = None) -> Dict[str, Any]:
    """Minimal order.placed payload matching contracts/events/order-placed.v1.schema.json."""
    return {
        "order_id": order_id,
        "customer_id": customer_id,
        "status": "PAID",
        "pickup_at": pickup_at,
        "total_amount_minor": 8500,
        "currency": "THB",
        "items": [
            {
                "flavor_id": flavor_id or str(uuid.uuid4()),
                "flavor_name": "E2E Pistachio",
                "portions": 1,
                "unit_price_minor": 8500,
                "subtotal_minor": 8500,
            }
        ],
    }


def queue_depth(queue: str) -> int:
    """Number of ready messages in a queue of the test vhost."""
    res = requests.get(f"{RABBITMQ_MGMT_URL}/api/queues/{RABBITMQ_VHOST}/{queue}", auth=_auth(), timeout=5.0)
    res.raise_for_status()
    return int(res.json().get("messages", 0))
