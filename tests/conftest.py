"""
Pytest global configuration and fixtures for GelatoFlow E2E Integration Testing.
"""

import datetime
import os
import time
import uuid
import pytest
import requests

from tests.helpers.api_client import ApiClient, Actor, mint_jwt

DEFAULT_GATEWAY_URL = os.getenv("GATEWAY_URL", "http://localhost:8080")
DEFAULT_JWT_SECRET = os.getenv("JWT_SECRET", "development_only_change_this_secret_32_bytes")
DEFAULT_WEBHOOK_SECRET = os.getenv("STRIPE_WEBHOOK_SECRET", "whsec_test_secret_for_integration_testing_12345")


def pytest_configure(config):
    """Register custom markers."""
    config.addinivalue_line("markers", "uc01: Mark test as Use Case 1 Preorder Checkout")
    config.addinivalue_line("markers", "uc02: Mark test as Use Case 2 Kitchen Fulfillment")
    config.addinivalue_line("markers", "uc03: Mark test as Use Case 3 Batch Inventory Management")
    config.addinivalue_line("markers", "nfr02: Mark test as NFR-02 Concurrency and Overselling Prevention")
    config.addinivalue_line("markers", "nfr_slot: Mark test as Time-Slot Capacity Protection")
    config.addinivalue_line("markers", "e2e: Mark test as End-to-End full system test")
    config.addinivalue_line("markers", "no_gateway_check: Test runs offline without gateway health check")


@pytest.fixture(scope="session")
def base_url() -> str:
    """Returns the API Gateway base URL."""
    return DEFAULT_GATEWAY_URL


@pytest.fixture(scope="session")
def api_client(base_url) -> ApiClient:
    """Returns an ApiClient pointing to API Gateway."""
    return ApiClient(base_url=base_url)


@pytest.fixture(scope="session")
def check_gateway_available(api_client) -> bool:
    """Verifies gateway availability before running suite."""
    try:
        res = api_client.get("/health", timeout=2.0)
        return res.status_code == 200
    except requests.RequestException:
        return False


@pytest.fixture(autouse=True)
def require_gateway(request, check_gateway_available):
    """
    Conditionally skip live tests if the gateway container is not currently running.
    Allows tests to gracefully skip in CI environments without active docker containers.
    """
    if "no_gateway_check" in request.keywords:
        return
    if not check_gateway_available:
        pytest.skip("GelatoFlow API Gateway is not reachable at configured GATEWAY_URL. Start containers with `docker compose up`.")


@pytest.fixture(scope="session")
def customer_actor(api_client) -> Actor:
    """Provides an authenticated Customer actor."""
    return api_client.create_minted_actor(role="CUSTOMER", name_prefix="Customer")


@pytest.fixture(scope="session")
def staff_actor(api_client) -> Actor:
    """Provides an authenticated Staff actor."""
    return api_client.create_minted_actor(role="STAFF", name_prefix="Kitchen Staff")


@pytest.fixture(scope="session")
def manager_actor(api_client) -> Actor:
    """Provides an authenticated Manager actor."""
    return api_client.create_minted_actor(role="MANAGER", name_prefix="Store Manager")


@pytest.fixture
def aligned_pickup_time() -> str:
    """
    Generates an RFC 3339 timestamp aligned to a 15-minute boundary in the future.
    Required by GelatoFlow pickup-slot validator.
    """
    future = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(hours=2)
    # Round down to nearest 15 minutes
    discard = datetime.timedelta(
        minutes=future.minute % 15,
        seconds=future.second,
        microseconds=future.microsecond,
    )
    aligned = future - discard
    return aligned.strftime("%Y-%m-%dT%H:%M:%SZ")


@pytest.fixture
def sample_flavor_id(api_client, customer_actor) -> str:
    """
    Discovers an active flavor ID from /api/v1/flavors, or returns a standard UUID fallback.
    """
    try:
        res = api_client.get("/api/v1/flavors", actor=customer_actor, timeout=3.0)
        if res.status_code == 200:
            items = res.json().get("items", [])
            if items:
                return items[0].get("id")
    except Exception:
        pass
    # Fallback known seed flavor ID
    return "9e914b8c-2476-4c28-8a7d-36c88b1f2b9d"
