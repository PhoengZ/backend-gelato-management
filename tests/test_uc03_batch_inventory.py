"""
Use Case 3: Batch Inventory Management & Waste Recording Integration Test.
Verifies:
1. Batch creation with MANAGER role
2. RBAC check (CUSTOMER/STAFF role rejected with 403 Forbidden for batch creation)
3. Reading batch details and current balances
4. Waste recording with idempotency key
5. Portions balance deduction (initial -> available - wasted)
6. Over-wasting protection (409 Conflict when waste exceeds available portions)
"""

import datetime
import uuid
import pytest
from tests.helpers.api_client import ApiClient, Actor


@pytest.mark.uc03
@pytest.mark.e2e
class TestUC03BatchInventory:
    """End-to-end integration tests for batch inventory lifecycle and waste tracking."""

    def test_manager_batch_creation_and_rbac(
        self,
        api_client: ApiClient,
        manager_actor: Actor,
        customer_actor: Actor,
        staff_actor: Actor,
        sample_flavor_id: str,
    ):
        """Only MANAGER can create new batches; CUSTOMER and STAFF are denied."""
        batch_code = f"TEST-{uuid.uuid4().hex[:6].upper()}"
        today = datetime.date.today().isoformat()
        expiry = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=7)).strftime("%Y-%m-%dT%H:%M:%SZ")

        payload = {
            "batch_code": batch_code,
            "flavor_id": sample_flavor_id,
            "production_date": today,
            "expires_at": expiry,
            "initial_portions": 50,
            "unit_cost_minor": 15000,
            "currency": "THB",
        }

        # 1. CUSTOMER forbidden
        res_cust = api_client.post("/api/v1/inventory/batches", actor=customer_actor, json_data=payload)
        assert res_cust.status_code == 403, f"Expected 403 for Customer, got {res_cust.status_code}"

        # 2. STAFF forbidden
        res_staff = api_client.post("/api/v1/inventory/batches", actor=staff_actor, json_data=payload)
        assert res_staff.status_code == 403, f"Expected 403 for Staff, got {res_staff.status_code}"

        # 3. MANAGER succeeds
        res_mgr = api_client.post("/api/v1/inventory/batches", actor=manager_actor, json_data=payload)
        assert res_mgr.status_code == 201, f"Manager failed to create batch: {res_mgr.status_code} {res_mgr.text}"
        batch = res_mgr.json()
        assert batch["batch_code"] == batch_code
        assert batch["initial_portions"] == 50
        assert batch["available_portions"] == 50
        assert batch["status"] == "ACTIVE"

    def test_waste_recording_deducts_portions(
        self,
        api_client: ApiClient,
        manager_actor: Actor,
        sample_flavor_id: str,
    ):
        """Recording waste deducts portions from available stock and updates wasted_portions."""
        # 1. Create a batch with 30 portions
        batch_code = f"WASTE-{uuid.uuid4().hex[:6].upper()}"
        today = datetime.date.today().isoformat()
        expiry = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=3)).strftime("%Y-%m-%dT%H:%M:%SZ")

        create_res = api_client.post(
            "/api/v1/inventory/batches",
            actor=manager_actor,
            json_data={
                "batch_code": batch_code,
                "flavor_id": sample_flavor_id,
                "production_date": today,
                "expires_at": expiry,
                "initial_portions": 30,
                "unit_cost_minor": 12000,
                "currency": "THB",
            },
        )
        assert create_res.status_code == 201
        batch_id = create_res.json()["id"]

        # 2. Record 5 portions wasted (e.g. spilled or expired)
        waste_key = str(uuid.uuid4())
        waste_res = api_client.post(
            f"/api/v1/inventory/batches/{batch_id}/waste",
            actor=manager_actor,
            json_data={
                "portions": 5,
                "reason": "QUALITY",
                "note": "Temperature excursion testing",
                "idempotency_key": waste_key,
            },
        )
        assert waste_res.status_code == 201, f"Failed to record waste: {waste_res.text}"
        waste_data = waste_res.json()
        assert waste_data["portions"] == 5
        assert waste_data["reason"] == "QUALITY"

        # 3. Read batch balance to confirm deduction
        get_res = api_client.get(f"/api/v1/inventory/batches/{batch_id}", actor=manager_actor)
        assert get_res.status_code == 200
        updated_batch = get_res.json()
        assert updated_batch["available_portions"] == 25
        assert updated_batch["wasted_portions"] == 5

        # 4. Attempt to waste more than remaining available portions (e.g. 50 portions)
        overwaste_res = api_client.post(
            f"/api/v1/inventory/batches/{batch_id}/waste",
            actor=manager_actor,
            json_data={
                "portions": 50,
                "reason": "DAMAGED",
                "idempotency_key": str(uuid.uuid4()),
            },
        )
        assert overwaste_res.status_code == 409, f"Expected 409 for excess waste, got {overwaste_res.status_code}"
