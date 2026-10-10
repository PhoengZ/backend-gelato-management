#!/usr/bin/env python3
"""Prepare the Auth-only Round 2 demo without editing teammates' services."""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import secrets
import shlex
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

DEMO = Path(__file__).resolve().parent
SERVICE = DEMO.parent
LOCAL = DEMO / ".local"
CONFIG = LOCAL / "config.json"
ENV_FILE = LOCAL / "compose.env"
AUTH_URL = "http://127.0.0.1:17011"
PROJECT = "gelatoflow-auth-demo"


class DemoError(Exception):
    pass


def private_write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(path.parent, 0o700)
    temporary = path.with_name(path.name + ".tmp-" + uuid.uuid4().hex)
    try:
        descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as stream:
            stream.write(text)
        os.replace(temporary, path)
        os.chmod(path, 0o600)
    finally:
        if temporary.exists():
            temporary.unlink()


def private_json(path, value):
    private_write(path, json.dumps(value, indent=2) + "\n")


def imported_jwt(path):
    """Read only JWT settings, never evaluate a shell environment file."""
    values = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:]
        key, separator, raw = line.partition("=")
        if separator and key in ("JWT_SECRET", "JWT_ISSUER", "JWT_AUDIENCE"):
            parsed = shlex.split(raw, comments=True)
            if len(parsed) != 1:
                raise DemoError("JWT configuration must contain a single literal value per setting.")
            values[key] = parsed[0]
    secret = values.get("JWT_SECRET", "")
    if len(secret.encode()) < 32 or any(c in secret for c in "\r\n"):
        raise DemoError("The supplied JWT configuration has no valid signing secret.")
    return {
        "AUTH_DEMO_JWT_SECRET": secret,
        "AUTH_DEMO_JWT_ISSUER": values.get("JWT_ISSUER", "gelatoflow-auth"),
        "AUTH_DEMO_JWT_AUDIENCE": values.get("JWT_AUDIENCE", "gelatoflow-api"),
    }


def configuration(create=False, jwt_env_file=None):
    imported = imported_jwt(jwt_env_file) if jwt_env_file else None
    if not CONFIG.exists():
        if not create:
            raise DemoError("Run 'up' first to prepare the private local configuration.")
        value = {
            "AUTH_DEMO_DB_PASSWORD": secrets.token_hex(32),
            "AUTH_DEMO_JWT_SECRET": secrets.token_hex(32),
            "AUTH_DEMO_JWT_ISSUER": "gelatoflow-auth",
            "AUTH_DEMO_JWT_AUDIENCE": "gelatoflow-api",
        }
        if imported:
            value.update(imported)
        private_json(CONFIG, value)
    value = json.loads(CONFIG.read_text())
    if imported and any(value.get(key) != item for key, item in imported.items()):
        raise DemoError("This demo already has a different JWT configuration. Preserve it or prepare a separate integration stack.")
    for key in ("AUTH_DEMO_DB_PASSWORD", "AUTH_DEMO_JWT_SECRET"):
        if not isinstance(value.get(key), str) or len(value[key].encode()) < 32:
            raise DemoError("Private local configuration is invalid.")
    if not all(isinstance(value.get(key), str) and value[key].strip() for key in ("AUTH_DEMO_JWT_ISSUER", "AUTH_DEMO_JWT_AUDIENCE")):
        raise DemoError("JWT issuer and audience must be non-empty.")
    for item in value.values():
        if not isinstance(item, str) or any(c in item for c in "\r\n"):
            raise DemoError("Private local configuration contains an invalid value.")
    private_write(ENV_FILE, "".join(f"{key}={json.dumps(item)}\n" for key, item in value.items()))
    # Frontend backend mode needs an Auth origin, not a signing secret.
    private_write(LOCAL / "frontend.env", "AUTH_MODE=backend\nAUTH_SERVICE_URL=" + AUTH_URL + "\nJWT_ISSUER=" + value["AUTH_DEMO_JWT_ISSUER"] + "\nJWT_AUDIENCE=" + value["AUTH_DEMO_JWT_AUDIENCE"] + "\nNEXT_PUBLIC_API_BASE_URL=\n")
    private_write(LOCAL / "server-jwt.env", "# SERVER ONLY: configure the team's JWT verifiers; never send this to a browser.\nJWT_SECRET=" + json.dumps(value["AUTH_DEMO_JWT_SECRET"]) + "\nJWT_ISSUER=" + value["AUTH_DEMO_JWT_ISSUER"] + "\nJWT_AUDIENCE=" + value["AUTH_DEMO_JWT_AUDIENCE"] + "\nAUTH_SERVICE_URL=" + AUTH_URL + "\n")
    os.chmod(CONFIG, 0o600)
    return value


def sensitive_values(value):
    found = [value["AUTH_DEMO_DB_PASSWORD"], value["AUTH_DEMO_JWT_SECRET"]]
    credentials = LOCAL / "credentials.json"
    if credentials.exists():
        try:
            accounts = json.loads(credentials.read_text())
            for role in ("customer", "staff", "manager"):
                for key in ("password", "access_token"):
                    secret = accounts.get(role, {}).get(key)
                    if isinstance(secret, str) and secret:
                        found.append(secret)
        except (OSError, ValueError, AttributeError):
            pass
    return found


def execute(command, value, *, cwd=SERVICE, env=None, show=True):
    result = subprocess.run(command, cwd=cwd, env=env, text=True, capture_output=True)
    output = result.stdout + result.stderr
    for secret in sensitive_values(value):
        output = output.replace(secret, "[REDACTED]")
    if show and output:
        print(output, end="" if output.endswith("\n") else "\n")
    if result.returncode:
        if not show and output:
            print(output, file=sys.stderr)
        raise DemoError(f"Command failed with exit {result.returncode}; credentials are hidden.")
    return result.stdout


def compose(value, *arguments, show=True):
    return execute(["docker", "compose", "--project-name", PROJECT, "--env-file", str(ENV_FILE), "-f", str(DEMO / "compose.yml"), *arguments], value, show=show)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def http(method, path, *, body=None, token=None):
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    raw = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        raw = json.dumps(body).encode()
    request = urllib.request.Request(AUTH_URL + path, data=raw, headers=headers, method=method)
    try:
        response = urllib.request.build_opener(NoRedirect).open(request, timeout=8)
    except urllib.error.HTTPError as error:
        response = error
    except (OSError, urllib.error.URLError) as error:
        raise DemoError("Auth is unreachable; run 'up' first.") from error
    with response:
        status = response.status
        data = response.read(65537)
    if len(data) > 65536:
        raise DemoError("Auth response exceeded the expected size.")
    try:
        return status, json.loads(data)
    except ValueError as error:
        raise DemoError("Auth returned an unexpected response; body omitted.") from error


def up(value, no_build=False):
    compose(value, "config", "--quiet", show=False)
    args = ["up", "-d"]
    if not no_build:
        args.append("--build")
    compose(value, *args)
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        try:
            status, _ = http("GET", "/health")
            if status == 200:
                print("READY: Auth http://127.0.0.1:17011; isolated PostgreSQL 127.0.0.1:17433/auth_demo.")
                return
        except DemoError:
            pass
        time.sleep(1)
    raise DemoError("Auth did not become ready. Run 'logs' to inspect sanitized output.")


def seed(value, verify_only=False):
    env = os.environ.copy()
    env.update({
        "AUTH_DEMO_URL": AUTH_URL,
        "AUTH_DEMO_DATABASE_URL": "postgres://auth_demo:" + value["AUTH_DEMO_DB_PASSWORD"] + "@127.0.0.1:17433/auth_demo?sslmode=disable",
        "AUTH_DEMO_STATE_DIR": str(LOCAL),
    })
    args = ["go", "run", "./cmd/demo"]
    if verify_only:
        args.append("--verify-only")
    execute(args, value, env=env)
    accounts = json.loads((LOCAL / "credentials.json").read_text())
    values = [{"key": "authBaseUrl", "value": AUTH_URL, "enabled": True}]
    for role in ("customer", "staff", "manager"):
        for label, key in (("Email", "email"), ("Password", "password"), ("Token", "access_token"), ("Id", "id")):
            values.append({"key": role + label, "value": accounts[role][key], "enabled": True, "type": "secret" if label in ("Password", "Token") else "default"})
    private_json(LOCAL / "postman.environment.json", {"id": str(uuid.uuid4()), "name": "GelatoFlow Auth local private", "values": values, "_postman_variable_scope": "environment"})


def demonstrate(value, pause=False):
    seed(value)
    print("Auth-issued JWTs verified for CUSTOMER, STAFF and MANAGER; credentials stay in demo/.local.")
    if pause:
        input("Press Enter to show rejected requests... ")
    accounts = json.loads((LOCAL / "credentials.json").read_text())
    customer = accounts["customer"]
    status, _ = http("POST", "/api/v1/auth/login", body={"email": customer["email"], "password": secrets.token_urlsafe(32)})
    if status != 401:
        raise DemoError("Incorrect-password login did not return 401.")
    print("Incorrect password: HTTP 401")
    status, _ = http("GET", "/api/v1/auth/me")
    if status != 401:
        raise DemoError("Anonymous /me did not return 401.")
    print("No bearer token: /me HTTP 401")
    status, _ = http("POST", "/api/v1/auth/signup", body={"email": "demo-injection-" + uuid.uuid4().hex + "@example.test", "password": secrets.token_urlsafe(24), "display_name": "Rejected role injection", "role": "MANAGER"})
    if status != 400:
        raise DemoError("Public signup accepted a client-supplied role.")
    print("Client-supplied role at public signup: HTTP 400")
    private_json(LOCAL / "boundary-verification.json", {"recorded_at": datetime.now(timezone.utc).isoformat(), "status": "PASS", "incorrect_password": 401, "anonymous_me": 401, "signup_role_injection": 400, "locally_minted_jwt": False, "scope": "Auth only; no full UC1–UC3 integration claim"})


def web(value, directory, port):
    next_binary = directory / "node_modules" / ".bin" / "next"
    if not next_binary.exists():
        raise DemoError("The frontend needs its existing Node dependencies before starting the Auth UI.")
    env = os.environ.copy()
    env.update({"AUTH_MODE": "backend", "AUTH_SERVICE_URL": AUTH_URL, "JWT_ISSUER": value["AUTH_DEMO_JWT_ISSUER"], "JWT_AUDIENCE": value["AUTH_DEMO_JWT_AUDIENCE"], "NEXT_PUBLIC_API_BASE_URL": ""})
    print(f"Auth UI: http://127.0.0.1:{port}/login (real backend mode). Stop this process with Ctrl-C.")
    result = subprocess.run([str(next_binary), "dev", "--hostname", "127.0.0.1", "--port", str(port)], cwd=directory, env=env)
    if result.returncode not in (0, 130):
        raise DemoError("Frontend process stopped; inspect its local output.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    start = actions.add_parser("up", help="Build/start only Auth and its isolated PostgreSQL")
    start.add_argument("--no-build", action="store_true")
    start.add_argument("--jwt-env-file", type=Path, help="Import existing local server JWT settings on the first start")
    actions.add_parser("seed", help="Prepare three persistent demo roles through real signup/login")
    actions.add_parser("verify", help="Login and verify existing accounts without provisioning roles")
    demo = actions.add_parser("demo", help="Refresh real tokens and demonstrate Auth failures")
    demo.add_argument("--pause", action="store_true")
    actions.add_parser("status", help="Show only this stack and Auth liveness")
    actions.add_parser("logs", help="Read only this stack's sanitized logs")
    actions.add_parser("down", help="Stop only this stack; preserve its database and local credentials")
    ui = actions.add_parser("web", help="Start the existing frontend in real backend Auth mode")
    ui.add_argument("--frontend-dir", type=Path, default=SERVICE.parent.parent / "frontend-gelato-management")
    ui.add_argument("--port", type=int, default=3101)
    args = parser.parse_args()
    value = configuration(create=args.action == "up", jwt_env_file=getattr(args, "jwt_env_file", None))
    if args.action == "up":
        up(value, args.no_build)
    elif args.action == "seed":
        seed(value)
    elif args.action == "verify":
        seed(value, verify_only=True)
    elif args.action == "demo":
        demonstrate(value, args.pause)
    elif args.action == "status":
        compose(value, "ps")
        status, _ = http("GET", "/health")
        print(f"Auth /health: HTTP {status}")
    elif args.action == "logs":
        compose(value, "logs", "--tail", "80", "auth", "auth-migrate")
    elif args.action == "down":
        compose(value, "down")
        print("Stopped only gelatoflow-auth-demo; database volume and fixture credentials are retained.")
    elif args.action == "web":
        if not 1 <= args.port <= 65535:
            raise DemoError("Frontend port must be between 1 and 65535.")
        web(value, args.frontend_dir.resolve(), args.port)


if __name__ == "__main__":
    try:
        main()
    except (DemoError, OSError, ValueError, KeyError) as error:
        # Never print exception data that might contain credentials or responses.
        message = str(error) if isinstance(error, DemoError) else "Local demo operation failed; check the private configuration and local files."
        print("ERROR: " + message, file=sys.stderr)
        raise SystemExit(1)
    except KeyboardInterrupt:
        print("Stopped locally; no data was deleted.", file=sys.stderr)
        raise SystemExit(130)
