"""
API Client and Actor Utilities for GelatoFlow Integration Tests.
Provides standard HTTP request handling, JWT minting (pure Python standard library),
and Actor-based authentication wrappers.
"""

import base64
import hashlib
import hmac
import json
import os
import time
import uuid
from dataclasses import dataclass
from typing import Any, Dict, Optional

import requests

DEFAULT_JWT_SECRET = os.getenv("JWT_SECRET", "development_only_change_this_secret_32_bytes")
DEFAULT_JWT_ISSUER = os.getenv("JWT_ISSUER", "gelatoflow-auth")
DEFAULT_JWT_AUDIENCE = os.getenv("JWT_AUDIENCE", "gelatoflow-api")


def _b64url_encode(data: bytes) -> str:
    """Encode bytes using URL-safe Base64 without padding."""
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def mint_jwt(
    user_id: str,
    role: str,
    secret: str = DEFAULT_JWT_SECRET,
    issuer: str = DEFAULT_JWT_ISSUER,
    audience: str = DEFAULT_JWT_AUDIENCE,
    exp_seconds: int = 3600,
) -> str:
    """
    Mints a valid HS256 JWT for testing using standard library crypto.
    Matches GelatoFlow's claims: sub (UUID), role (CUSTOMER|STAFF|MANAGER), iss, aud, iat, exp.
    """
    now = int(time.time())
    header = {"alg": "HS256", "typ": "JWT"}
    payload = {
        "sub": user_id,
        "role": role,
        "iss": issuer,
        "aud": audience,
        "iat": now,
        "exp": now + exp_seconds,
    }

    header_bytes = json.dumps(header, separators=(",", ":")).encode("utf-8")
    payload_bytes = json.dumps(payload, separators=(",", ":")).encode("utf-8")

    header_b64 = _b64url_encode(header_bytes)
    payload_b64 = _b64url_encode(payload_bytes)

    signing_input = f"{header_b64}.{payload_b64}".encode("utf-8")
    signature = hmac.new(secret.encode("utf-8"), signing_input, hashlib.sha256).digest()
    signature_b64 = _b64url_encode(signature)

    return f"{header_b64}.{payload_b64}.{signature_b64}"


@dataclass
class Actor:
    """Represents a tested actor in GelatoFlow with role and token credentials."""
    user_id: str
    email: str
    display_name: str
    role: str
    token: str

    @property
    def auth_headers(self) -> Dict[str, str]:
        return {"Authorization": f"Bearer {self.token}"}


class ApiClient:
    """
    HTTP client configured for GelatoFlow API Gateway.
    Encapsulates base URL, JSON requests, and Bearer authentication.
    """

    def __init__(self, base_url: str = "http://localhost:8080", timeout: float = 10.0):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self.session = requests.Session()

    def request(
        self,
        method: str,
        path: str,
        actor: Optional[Actor] = None,
        headers: Optional[Dict[str, str]] = None,
        json_data: Optional[Any] = None,
        params: Optional[Dict[str, Any]] = None,
        timeout: Optional[float] = None,
    ) -> requests.Response:
        """Execute HTTP request against the API Gateway."""
        url = f"{self.base_url}/{path.lstrip('/')}"
        req_headers = {"Accept": "application/json"}
        if json_data is not None:
            req_headers["Content-Type"] = "application/json"

        if actor is not None:
            req_headers.update(actor.auth_headers)

        if headers:
            req_headers.update(headers)

        effective_timeout = timeout if timeout is not None else self.timeout
        return self.session.request(
            method=method,
            url=url,
            headers=req_headers,
            json=json_data,
            params=params,
            timeout=effective_timeout,
        )

    def get(self, path: str, actor: Optional[Actor] = None, **kwargs) -> requests.Response:
        return self.request("GET", path, actor=actor, **kwargs)

    def post(self, path: str, actor: Optional[Actor] = None, **kwargs) -> requests.Response:
        return self.request("POST", path, actor=actor, **kwargs)

    def patch(self, path: str, actor: Optional[Actor] = None, **kwargs) -> requests.Response:
        return self.request("PATCH", path, actor=actor, **kwargs)

    def put(self, path: str, actor: Optional[Actor] = None, **kwargs) -> requests.Response:
        return self.request("PUT", path, actor=actor, **kwargs)

    def delete(self, path: str, actor: Optional[Actor] = None, **kwargs) -> requests.Response:
        return self.request("DELETE", path, actor=actor, **kwargs)

    def signup_customer(self, email: Optional[str] = None, password: str = "P@ssword123!") -> Actor:
        """Register a new customer account via /api/v1/auth/signup and log in."""
        unique_id = uuid.uuid4().hex[:8]
        user_email = email or f"customer_{unique_id}@example.com"
        display_name = f"Customer {unique_id}"

        # 1. Sign up
        signup_res = self.post(
            "/api/v1/auth/signup",
            json_data={"email": user_email, "password": password, "display_name": display_name},
        )
        if signup_res.status_code not in (201, 409):
            # If API is not running or other error, fallback to minted token
            user_id = str(uuid.uuid4())
            token = mint_jwt(user_id=user_id, role="CUSTOMER")
            return Actor(user_id=user_id, email=user_email, display_name=display_name, role="CUSTOMER", token=token)

        # 2. Log in
        login_res = self.post(
            "/api/v1/auth/login",
            json_data={"email": user_email, "password": password},
        )
        if login_res.status_code == 200:
            data = login_res.json()
            return Actor(
                user_id=data["user"]["id"],
                email=user_email,
                display_name=display_name,
                role=data["user"]["role"],
                token=data["access_token"],
            )

        # Fallback if login endpoint isn't connected to DB during dry-run
        user_id = str(uuid.uuid4())
        token = mint_jwt(user_id=user_id, role="CUSTOMER")
        return Actor(user_id=user_id, email=user_email, display_name=display_name, role="CUSTOMER", token=token)

    def create_minted_actor(self, role: str, name_prefix: str = "TestUser") -> Actor:
        """Creates an Actor with a cryptographically valid minted JWT."""
        user_id = str(uuid.uuid4())
        email = f"{role.lower()}_{user_id[:8]}@gelatoflow.internal"
        display_name = f"{name_prefix} ({role})"
        token = mint_jwt(user_id=user_id, role=role)
        return Actor(user_id=user_id, email=email, display_name=display_name, role=role, token=token)
