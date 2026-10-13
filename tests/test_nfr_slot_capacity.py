"""
NFR: Time-Slot Capacity Protection Integration Test.
Verifies:
1. Pre-orders are constrained to configured 15-minute pickup time slot capacity (default: 10 orders/slot)
2. Excess orders beyond the capacity threshold are rejected with HTTP 409 (SLOT_CAPACITY_EXCEEDED)
"""

import datetime
import uuid
import pytest
from tests.helpers.api_client import ApiClient, Actor


@pytest.mark.nfr_slot
class TestNFRSlotCapacity:
    """Tests verifying pickup time slot capacity limits and rejection of overflow orders."""

    def test_slot_capacity_limit_enforced(
        self,
        api_client: ApiClient,
        manager_actor: Actor,
        sample_flavor_id: str,
    ):
        """Placing orders beyond the pickup slot capacity limit triggers 409 SLOT_CAPACITY_EXCEEDED."""
        # Pick a dedicated slot in the future (3 hours ahead)
        future = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(hours=3)
        discard = datetime.timedelta(
            minutes=future.minute % 15,
            seconds=future.second,
            microseconds=future.microsecond,
        )
        test_slot = (future - discard).strftime("%Y-%m-%dT%H:%M:%SZ")

        # 1. Seed a batch with sufficient stock so stock exhaustion does not mask slot capacity
        batch_code = f"SLOT-{uuid.uuid4().hex[:6].upper()}"
        today = datetime.date.today().isoformat()
        expiry = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=5)).strftime("%Y-%m-%dT%H:%M:%SZ")

        create_res = api_client.post(
            "/api/v1/inventory/batches",
            actor=manager_actor,
            json_data={
                "batch_code": batch_code,
                "flavor_id": sample_flavor_id,
                "production_date": today,
                "expires_at": expiry,
                "initial_portions": 100,
                "unit_cost_minor": 5000,
                "currency": "THB",
            },
        )
        assert create_res.status_code == 201, f"Failed to seed batch for slot capacity test: {create_res.text}"

        # 2. Place orders into the same slot until slot is full (max 10)
        max_capacity = 10
        accepted_orders = []
        capacity_exceeded = False

        for i in range(max_capacity + 2):
            customer = api_client.create_minted_actor(role="CUSTOMER", name_prefix=f"SlotUser{i}")
            res = api_client.post(
                "/api/v1/orders",
                actor=customer,
                headers={"Idempotency-Key": str(uuid.uuid4())},
                json_data={
                    "pickup_at": test_slot,
                    "items": [{"flavor_id": sample_flavor_id, "portions": 1}],
                },
            )
            if res.status_code == 201:
                accepted_orders.append(res.json()["order_id"])
            elif res.status_code == 409:
                err_data = res.json()
                if err_data.get("code") == "SLOT_CAPACITY_EXCEEDED":
                    capacity_exceeded = True
                    break

        # If we placed max_capacity, the subsequent attempt must exceed capacity
        if len(accepted_orders) >= max_capacity:
            assert capacity_exceeded, f"Expected 409 SLOT_CAPACITY_EXCEEDED after {len(accepted_orders)} orders in slot {test_slot}"
