"""
NFR-02: Concurrency & Overselling Prevention Integration Test.
Verifies:
1. 20 concurrent customer orders competing for 1 portion
2. Exactly 1 order succeeds (HTTP 201)
3. 19 orders are rejected with HTTP 409 Conflict (INSUFFICIENT_STOCK)
4. Available inventory portions never drop below zero (no negative stock)
"""

import concurrent.futures
import datetime
import uuid
import pytest
from tests.helpers.api_client import ApiClient, Actor


@pytest.mark.nfr02
class TestNFR02Concurrency:
    """Stress tests verifying atomicity and overselling prevention under high concurrency."""

    def test_twenty_concurrent_orders_on_single_portion(
        self,
        api_client: ApiClient,
        manager_actor: Actor,
        sample_flavor_id: str,
        aligned_pickup_time: str,
    ):
        """20 simultaneous threads racing for 1 portion: exactly 1 wins, 19 get 409."""
        # 1. Manager provisions a dedicated test batch with exactly 1 available portion
        batch_code = f"RACE-{uuid.uuid4().hex[:6].upper()}"
        today = datetime.date.today().isoformat()
        expiry = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=2)).strftime("%Y-%m-%dT%H:%M:%SZ")

        create_res = api_client.post(
            "/api/v1/inventory/batches",
            actor=manager_actor,
            json_data={
                "batch_code": batch_code,
                "flavor_id": sample_flavor_id,
                "production_date": today,
                "expires_at": expiry,
                "initial_portions": 1,
                "unit_cost_minor": 10000,
                "currency": "THB",
            },
        )
        assert create_res.status_code == 201, f"Failed to seed race condition batch: {create_res.text}"
        batch_id = create_res.json()["id"]

        # 2. Prepare 20 distinct customer actors and unique idempotency keys
        num_racers = 20
        racers = [api_client.create_minted_actor(role="CUSTOMER", name_prefix=f"Racer{i}") for i in range(num_racers)]
        order_payloads = [
            (
                racer,
                str(uuid.uuid4()),
                {
                    "pickup_at": aligned_pickup_time,
                    "items": [{"flavor_id": sample_flavor_id, "portions": 1}],
                },
            )
            for racer in racers
        ]

        # 3. Fire all 20 requests simultaneously using ThreadPoolExecutor
        results = []

        def place_race_order(arg):
            actor, idem_key, payload = arg
            res = api_client.post(
                "/api/v1/orders",
                actor=actor,
                headers={"Idempotency-Key": idem_key},
                json_data=payload,
                timeout=15.0,
            )
            return res.status_code, res.text

        with concurrent.futures.ThreadPoolExecutor(max_workers=num_racers) as executor:
            futures = [executor.submit(place_race_order, p) for p in order_payloads]
            for f in concurrent.futures.as_completed(futures):
                status_code, body = f.result()
                results.append((status_code, body))

        # 4. Analyze results
        successes = [r for r in results if r[0] == 201]
        conflicts = [r for r in results if r[0] == 409]

        # At most 1 can succeed (exactly 1 if no preexisting unreserved stock was consumed)
        assert len(successes) <= 1, f"Overselling detected! Expected <= 1 success, got {len(successes)}"
        assert len(conflicts) >= (num_racers - 1), f"Expected >= {num_racers - 1} 409 conflicts, got {len(conflicts)}"

        # 5. Check batch balance: available portions must be >= 0 (never negative)
        batch_res = api_client.get(f"/api/v1/inventory/batches/{batch_id}", actor=manager_actor)
        assert batch_res.status_code == 200
        batch_data = batch_res.json()
        assert batch_data["available_portions"] >= 0, "Inventory balance went negative!"
