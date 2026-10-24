"""
Fulfillment NFR Integration Tests.
Verifies, through the API Gateway and RabbitMQ:
1. Customers can read only their own ticket (queue number fallback when SSE fails)
2. Manager-only status history records the source event and the staff actor
3. Optional pickup window filter on the kitchen queue
4. order.cancelled processed before order.placed leaves no ticket
5. Duplicate order.placed delivery creates a single ticket
"""

import datetime
import random
import time
import uuid

import pytest

from tests.helpers.api_client import ApiClient, Actor
from tests.helpers.event_publisher import mgmt_available, order_placed_data, publish_order_event


@pytest.fixture(autouse=True)
def require_rabbitmq_mgmt():
    if not mgmt_available():
        pytest.skip("RabbitMQ management API is not reachable; set RABBITMQ_MGMT_URL / RABBITMQ_MGMT_USER / RABBITMQ_MGMT_PASS.")


def far_pickup_at() -> datetime.datetime:
    """A 15-minute aligned pickup on a random day far ahead, so tests do not share slots."""
    day = datetime.datetime.now(datetime.timezone.utc).replace(hour=0, minute=0, second=0, microsecond=0)
    return day + datetime.timedelta(days=random.randint(400, 4000), hours=14)


def rfc3339(t: datetime.datetime) -> str:
    return t.strftime("%Y-%m-%dT%H:%M:%SZ")


def wait_for_ticket(api_client: ApiClient, order_id: str, actor: Actor, timeout: float = 15.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        res = api_client.get(f"/api/v1/fulfillments/{order_id}", actor=actor)
        if res.status_code == 200:
            return res.json()
        time.sleep(0.5)
    pytest.fail(f"ticket for order {order_id} was not created within {timeout}s")


def place_order(api_client: ApiClient, staff: Actor, customer_id: str, pickup: datetime.datetime):
    order_id = str(uuid.uuid4())
    event_id = publish_order_event("order.placed", order_placed_data(order_id, customer_id, rfc3339(pickup)))
    return order_id, event_id, wait_for_ticket(api_client, order_id, staff)


@pytest.mark.fulfillment
@pytest.mark.e2e
class TestFulfillmentNFR:
    def test_customer_reads_only_own_ticket(self, api_client: ApiClient, customer_actor: Actor, staff_actor: Actor):
        order_id, _, ticket = place_order(api_client, staff_actor, customer_actor.user_id, far_pickup_at())

        own = api_client.get(f"/api/v1/fulfillments/{order_id}", actor=customer_actor)
        assert own.status_code == 200, own.text
        assert own.json()["queue_number"] == ticket["queue_number"]
        assert "customer_id" not in own.json()

        stranger = api_client.create_minted_actor(role="CUSTOMER", name_prefix="Other Customer")
        other = api_client.get(f"/api/v1/fulfillments/{order_id}", actor=stranger)
        assert other.status_code == 404, f"another customer must not see the ticket: {other.status_code}"

        listing = api_client.get("/api/v1/fulfillments", actor=customer_actor)
        assert listing.status_code == 403

    def test_history_is_manager_only_and_records_source_event(
        self, api_client: ApiClient, customer_actor: Actor, staff_actor: Actor, manager_actor: Actor
    ):
        order_id, event_id, _ = place_order(api_client, staff_actor, customer_actor.user_id, far_pickup_at())
        path = f"/api/v1/fulfillments/{order_id}/history"

        assert api_client.get(path, actor=staff_actor).status_code == 403

        res = api_client.get(path, actor=manager_actor)
        assert res.status_code == 200, res.text
        items = res.json()["items"]
        assert len(items) == 1
        assert items[0]["new_status"] == "PREPARING"
        assert items[0]["source_event_id"] == event_id

        ready = api_client.patch(
            f"/api/v1/fulfillments/{order_id}/status", actor=staff_actor, json_data={"status": "READY_FOR_PICKUP"}
        )
        assert ready.status_code == 200, ready.text

        items = api_client.get(path, actor=manager_actor).json()["items"]
        assert [i["new_status"] for i in items] == ["PREPARING", "READY_FOR_PICKUP"]
        assert items[1]["actor_id"] == staff_actor.user_id
        assert "source_event_id" not in items[1]

    def test_list_pickup_window_filter(self, api_client: ApiClient, customer_actor: Actor, staff_actor: Actor):
        pickup = far_pickup_at()
        order_id, _, _ = place_order(api_client, staff_actor, customer_actor.user_id, pickup)
        day = pickup.replace(hour=0)

        def listed(start: datetime.datetime, end: datetime.datetime):
            res = api_client.get(
                "/api/v1/fulfillments",
                actor=staff_actor,
                params={"pickup_from": rfc3339(start), "pickup_to": rfc3339(end)},
            )
            assert res.status_code == 200, res.text
            return [t["order_id"] for t in res.json()["items"]]

        assert order_id in listed(day, day + datetime.timedelta(days=1))
        assert order_id not in listed(day + datetime.timedelta(days=1), day + datetime.timedelta(days=2))

        bad = api_client.get("/api/v1/fulfillments", actor=staff_actor, params={"pickup_from": "yesterday"})
        assert bad.status_code == 400
        assert bad.json()["code"] == "VALIDATION_ERROR"

    def test_cancel_before_placed_creates_no_ticket(self, api_client: ApiClient, customer_actor: Actor, staff_actor: Actor):
        order_id = str(uuid.uuid4())
        publish_order_event("order.cancelled", {"order_id": order_id, "reason": "CUSTOMER_REQUEST"})
        time.sleep(1.0)  # let the cancellation be applied first
        publish_order_event("order.placed", order_placed_data(order_id, customer_actor.user_id, rfc3339(far_pickup_at())))

        time.sleep(5.0)
        res = api_client.get(f"/api/v1/fulfillments/{order_id}", actor=staff_actor)
        assert res.status_code == 404, f"a cancelled order must not get a ticket: {res.status_code} {res.text}"

    def test_duplicate_order_placed_creates_single_ticket(
        self, api_client: ApiClient, customer_actor: Actor, staff_actor: Actor
    ):
        pickup = far_pickup_at()
        order_id, event_id, first = place_order(api_client, staff_actor, customer_actor.user_id, pickup)
        data = order_placed_data(order_id, customer_actor.user_id, rfc3339(pickup))

        publish_order_event("order.placed", data, event_id=event_id)  # same delivery redelivered
        publish_order_event("order.placed", data)  # same order, new event id
        time.sleep(3.0)

        day = pickup.replace(hour=0)
        res = api_client.get(
            "/api/v1/fulfillments",
            actor=staff_actor,
            params={"pickup_from": rfc3339(day), "pickup_to": rfc3339(day + datetime.timedelta(days=1))},
        )
        tickets = [t for t in res.json()["items"] if t["order_id"] == order_id]
        assert len(tickets) == 1
        assert tickets[0]["queue_number"] == first["queue_number"]
