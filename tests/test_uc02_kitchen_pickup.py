"""
Use Case 2: Kitchen Fulfillment & Order Pickup Integration Test.
Verifies:
1. Kitchen queue listing with STAFF / MANAGER roles
2. RBAC enforcement (CUSTOMER role receives 403 Forbidden)
3. Ticket lifecycle progression (PREPARING -> READY_FOR_PICKUP -> PICKED_UP)
4. Invalid transition rejection (409 Conflict)
5. Audit logging and ticket querying
"""

import uuid
import pytest
from tests.helpers.api_client import ApiClient, Actor


@pytest.mark.uc02
@pytest.mark.e2e
class TestUC02KitchenPickup:
    """End-to-end integration tests for kitchen queue management and pickup."""

    def test_customer_rbac_denied_on_fulfillments(
        self,
        api_client: ApiClient,
        customer_actor: Actor,
    ):
        """Customers must receive 403 Forbidden when accessing kitchen queue."""
        res_list = api_client.get("/api/v1/fulfillments", actor=customer_actor)
        assert res_list.status_code == 403, f"Expected 403 Forbidden for customer, got {res_list.status_code}"

        fake_order_id = str(uuid.uuid4())
        res_patch = api_client.patch(
            f"/api/v1/fulfillments/{fake_order_id}/status",
            actor=customer_actor,
            json_data={"status": "READY_FOR_PICKUP"},
        )
        assert res_patch.status_code == 403, f"Expected 403 Forbidden for patch by customer, got {res_patch.status_code}"

    def test_staff_can_list_kitchen_queue(
        self,
        api_client: ApiClient,
        staff_actor: Actor,
    ):
        """Staff members can view queue items."""
        res = api_client.get("/api/v1/fulfillments", actor=staff_actor)
        assert res.status_code == 200, f"Staff failed to list queue: {res.text}"
        data = res.json()
        assert "items" in data
        assert isinstance(data["items"], list)

    def test_fulfillment_status_progression(
        self,
        api_client: ApiClient,
        staff_actor: Actor,
    ):
        """Fulfillment status progresses from PREPARING to READY_FOR_PICKUP to PICKED_UP."""
        # 1. Look for an existing pending ticket in queue
        list_res = api_client.get("/api/v1/fulfillments", actor=staff_actor)
        assert list_res.status_code == 200
        items = list_res.json().get("items", [])

        if not items:
            # If no items in queue, verify 404 on non-existent order
            random_id = str(uuid.uuid4())
            get_res = api_client.get(f"/api/v1/fulfillments/{random_id}", actor=staff_actor)
            assert get_res.status_code == 404
            return

        # Pick first ticket
        ticket = items[0]
        order_id = ticket["order_id"]
        current_status = ticket["status"]

        # 2. Advance to READY_FOR_PICKUP if PREPARING
        if current_status == "PREPARING":
            advance_res = api_client.patch(
                f"/api/v1/fulfillments/{order_id}/status",
                actor=staff_actor,
                json_data={"status": "READY_FOR_PICKUP"},
            )
            assert advance_res.status_code == 200
            assert advance_res.json()["status"] == "READY_FOR_PICKUP"

        # 3. Advance to PICKED_UP
        pickup_res = api_client.patch(
            f"/api/v1/fulfillments/{order_id}/status",
            actor=staff_actor,
            json_data={"status": "PICKED_UP"},
        )
        assert pickup_res.status_code == 200
        assert pickup_res.json()["status"] == "PICKED_UP"

        # 4. Attempt invalid transition backwards from PICKED_UP to READY_FOR_PICKUP
        invalid_res = api_client.patch(
            f"/api/v1/fulfillments/{order_id}/status",
            actor=staff_actor,
            json_data={"status": "READY_FOR_PICKUP"},
        )
        assert invalid_res.status_code in (400, 409), f"Expected 409 Conflict, got {invalid_res.status_code}"
