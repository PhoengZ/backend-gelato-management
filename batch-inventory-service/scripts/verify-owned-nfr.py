#!/usr/bin/env python3
"""Isolated owned-service rollout/TLS/compatibility verification.

Uses fresh Docker resources and synthetic identities only. Does not read local
.env files, migrate an existing database, install a host CA, or edit consumers.
"""

import argparse
import concurrent.futures
import datetime
import json
import os
from pathlib import Path
import shutil
import signal
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid


class VerificationError(Exception):
    pass


class Fixture:
    def __init__(self, root, work):
        self.root, self.work = root, work
        self.identity = "owned-nfr-" + uuid.uuid4().hex[:12]
        self.network = self.identity + "-network"
        self.containers, self.images = [], {}
        self.stage = "prepare"
        self.secret = "isolated_owned_nfr_jwt_" + uuid.uuid4().hex
        self.machine = "isolated_owned_order_" + uuid.uuid4().hex
        self.checks = {}
        self.private_values = [self.secret, self.machine]

    def run(self, args, *, success=True, env=None, timeout=300, log=None):
        result = subprocess.run(args, cwd=self.root, env=env, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
        if log:
            (self.work / log).write_text(result.stdout)
        if (result.returncode == 0) != success:
            raise VerificationError("command_failed")
        return result.stdout.strip()

    def docker(self, *args, **options):
        return self.run(["docker", *args], **options)

    def sql(self, query):
        return self.docker("exec", self.identity + "-pg", "psql", "-U", "fixture",
                           "-d", "fixture", "-v", "ON_ERROR_STOP=1", "-tAc", query)

    def start(self, suffix, image, env=None, ports=(), aliases=(), mounts=(), command=()):
        name = self.identity + "-" + suffix
        # Reserve the unique name before starting so partial start failures are
        # cleaned too. No existing/shared resource name is accepted as input.
        self.containers.append(name)
        args = ["run", "-d", "--rm", "--name", name, "--label", "codex.task=" + self.identity,
                "--network", self.network]
        for alias in aliases:
            args += ["--network-alias", alias]
        for port in ports:
            args += ["-p", "127.0.0.1::" + str(port)]
        for key, value in (env or {}).items():
            args += ["-e", key + "=" + str(value)]
        for source, target in mounts:
            args += ["--mount", "type=bind,src=" + str(source) + ",dst=" + target + ",readonly"]
        self.docker(*args, image, *command)
        return name

    def port(self, name, number):
        return int(self.docker("port", name, str(number) + "/tcp").rsplit(":", 1)[1])

    def request(self, url, method="GET", body=None, token=None, headers=None, expected=200, context=None):
        data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
        values = {"Content-Type": "application/json"}
        if token:
            values["Authorization"] = "Bearer " + token
        values.update(headers or {})
        request = urllib.request.Request(url, data=data, headers=values, method=method)
        try:
            with urllib.request.urlopen(request, context=context, timeout=8) as response:
                status, raw = response.status, response.read()
        except urllib.error.HTTPError as response:
            status, raw = response.code, response.read()
        if status != expected:
            raise VerificationError("unexpected_http_status_" + str(status))
        return json.loads(raw) if raw else None

    def ready(self, url, context=None):
        reason = "unavailable"
        for _ in range(100):
            try:
                self.request(url, context=context)
                return
            except (OSError, urllib.error.URLError, VerificationError) as error:
                cause = getattr(error, "reason", error)
                reason = type(cause).__name__ + "_" + str(getattr(cause, "verify_code", 0))
                time.sleep(0.2)
        raise VerificationError("readiness_timeout_" + reason)

    def certificates(self):
        self.stage = "local_certificates"
        ca_config = self.work / "ca.cnf"
        ca_config.write_text("[req]\nprompt=no\ndistinguished_name=dn\nx509_extensions=ca\n"
                             "[dn]\nCN=isolated-owned-nfr-ca\n[ca]\n"
                             "basicConstraints=critical,CA:TRUE,pathlen:0\n"
                             "keyUsage=critical,keyCertSign,cRLSign\nsubjectKeyIdentifier=hash\n"
                             "authorityKeyIdentifier=keyid:always\n")
        for name in ("ca", "other-ca"):
            self.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-sha256", "-days", "1",
                      "-keyout", str(self.work / (name + ".key")), "-out", str(self.work / (name + ".pem")),
                      "-config", str(ca_config)], log=name + "-setup.log")
        self.run(["openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes",
                  "-keyout", str(self.work / "server.key"), "-out", str(self.work / "server.csr"),
                  "-subj", "/CN=localhost"], log="server-request.log")
        extensions = self.work / "server.ext"
        extensions.write_text("subjectAltName=DNS:localhost,DNS:auth,DNS:catalog,DNS:inventory,IP:127.0.0.1\n"
                              "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\n"
                              "extendedKeyUsage=serverAuth\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid,issuer\n")
        self.run(["openssl", "x509", "-req", "-in", str(self.work / "server.csr"), "-CA", str(self.work / "ca.pem"),
                  "-CAkey", str(self.work / "ca.key"), "-CAcreateserial", "-days", "1", "-sha256",
                  "-extfile", str(extensions), "-out", str(self.work / "server.pem")], log="server-sign.log")
        self.run(["openssl", "verify", "-x509_strict", "-CAfile", str(self.work / "ca.pem"),
                  str(self.work / "server.pem")], log="strict-chain.log")
        for key in (self.work / "ca.key", self.work / "other-ca.key"):
            key.chmod(0o600)
        # This disposable leaf key is mounted into non-root test containers.
        # Its parent directory is private (0700); it is deleted on every exit.
        (self.work / "server.key").chmod(0o644)

    def build(self):
        self.stage = "build_actual_order_client_probe"
        probe = self.work / "order-probe"
        probe.mkdir()
        source = self.root / "batch-inventory-service/scripts/order_client_probe.go.txt"
        (probe / "main.go").write_bytes(source.read_bytes())
        (probe / "go.mod").write_text('module order-service/nfrprobe\n\ngo 1.25.0\n\nrequire order-service v0.0.0\n'
                                      'replace order-service => ' + json.dumps(str(self.root / "order-service")) + '\n')
        (probe / "go.sum").write_bytes((self.root / "order-service/go.sum").read_bytes())
        env = {**os.environ, "GOTOOLCHAIN": "local"}
        result = subprocess.run(["go", "build", "-mod=mod", "-o", str(self.work / "order-probe-bin"), "."],
                                cwd=probe, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=180)
        (self.work / "order-probe-build.log").write_text(result.stdout)
        if result.returncode:
            raise VerificationError("order_probe_build_failed")
        self.stage = "build_images"
        modules = ["auth-service", "catalog-service", "batch-inventory-service", "api-gateway"]
        def image(module):
            tag = "codex/" + self.identity + ":" + module
            self.images[module] = tag
            self.docker("build", "--label", "codex.task=" + self.identity, "-t", tag,
                        str(self.root / module), log=module + "-build.log", timeout=600)
            print(json.dumps({"build": module, "status": "passed"}), flush=True)
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            list(pool.map(image, modules))

    def prepare(self):
        self.stage = "isolated_dependencies"
        self.docker("network", "create", "--label", "codex.task=" + self.identity, self.network)
        self.start("pg", "postgres:16-alpine", {"POSTGRES_USER": "fixture", "POSTGRES_PASSWORD": "fixture_only",
                                               "POSTGRES_DB": "fixture"}, aliases=("postgres",))
        self.start("redis", "redis:7-alpine", aliases=("redis",))
        for _ in range(100):
            try:
                self.sql("SELECT 1")
                break
            except VerificationError:
                time.sleep(0.2)
        else:
            raise VerificationError("postgres_readiness_timeout")

    def rpc(self, name, port, body, token=None, tls=False, success=True, authority=None, ca="ca.pem"):
        args = ["grpcurl", "-max-time", "3", "-import-path", str(self.root / "contracts/proto"),
                "-proto", "inventory/v1/inventory.proto" if name.startswith("inventory.") else "catalog/v1/catalog.proto"]
        args += ["-cacert", str(self.work / ca)] if tls else ["-plaintext"]
        if authority:
            args += ["-authority", authority]
        if token:
            args += ["-H", "authorization: Bearer " + token]
        args += ["-d", json.dumps(body), "127.0.0.1:" + str(port), name]
        raw = self.run(args, success=success, timeout=8)
        return json.loads(raw) if success and raw else None

    def profile(self, tls):
        mode = "tls" if tls else "plain"
        self.stage = mode + "_rollout"
        start_index = len(self.containers)
        namespace = uuid.uuid4().hex
        auth_schema, inv_schema = "auth_" + namespace, "inventory_" + namespace
        self.sql("CREATE SCHEMA " + auth_schema + "; CREATE SCHEMA " + inv_schema)
        dsn = lambda schema: "postgres://fixture:fixture_only@postgres:5432/fixture?sslmode=disable&search_path=" + schema
        common = {"JWT_SECRET": self.secret, "JWT_ISSUER": "gelatoflow-auth", "JWT_AUDIENCE": "gelatoflow-api",
                  "REQUEST_TIMEOUT": "3s", "HTTP_TLS_CERT_FILE": "", "HTTP_TLS_KEY_FILE": "",
                  "GRPC_TLS_CERT_FILE": "", "GRPC_TLS_KEY_FILE": ""}
        mounts = []
        scheme, context = "http", None
        if tls:
            scheme, context = "https", ssl.create_default_context(cafile=str(self.work / "ca.pem"))
            common.update({"HTTP_TLS_CERT_FILE": "/tls/server.pem", "HTTP_TLS_KEY_FILE": "/tls/server.key",
                           "GRPC_TLS_CERT_FILE": "/tls/server.pem", "GRPC_TLS_KEY_FILE": "/tls/server.key"})
            mounts = [(self.work / f, "/tls/" + f) for f in ("server.pem", "server.key", "ca.pem")]
        inventory_env = {**common, "DATABASE_URL": dsn(inv_schema), "HTTP_ADDR": ":3004", "GRPC_ADDR": ":50051",
                         "CATALOG_GRPC_ADDR": "catalog:50052", "ORDER_SERVICE_TOKEN": self.machine, "PUBLISH_EVENTS": "false"}
        if tls:
            inventory_env.update({"CATALOG_GRPC_TLS_ENABLED": "true", "CATALOG_GRPC_TLS_CA_FILE": "/tls/ca.pem",
                                  "CATALOG_GRPC_TLS_SERVER_NAME": "catalog"})
        try:
            if not tls:
                args = ["run", "--rm", "--network", self.network]
                for k, v in inventory_env.items(): args += ["-e", k + "=" + v]
                text = self.docker(*args, self.images["batch-inventory-service"], success=False)
                if "migration" not in text.lower(): raise VerificationError("unmigrated_startup_was_not_blocked")
                self.checks["unmigrated_inventory_startup_blocked"] = "PASS"
            for _ in range(2):
                self.docker("run", "--rm", "--network", self.network, "-e", "DATABASE_URL=" + dsn(inv_schema),
                            self.images["batch-inventory-service"], "/app/migrate")
            auth = self.start("auth-" + mode, self.images["auth-service"],
                              {**common, "PORT": "3001", "DATABASE_URL": dsn(auth_schema), "BCRYPT_COST": "10"},
                              ports=(3001,), aliases=("auth",), mounts=mounts)
            catalog = self.start("catalog-" + mode, self.images["catalog-service"],
                                 {**common, "PORT": "3002", "GRPC_ADDR": ":50052", "REDIS_URL": "redis://redis:6379/0",
                                  "REDIS_KEY_PREFIX": "owned:" + namespace}, ports=(3002, 50052), aliases=("catalog",), mounts=mounts)
            inventory = self.start("inventory-" + mode, self.images["batch-inventory-service"], inventory_env,
                                   ports=(3004, 50051), aliases=("inventory",), mounts=mounts)
            gateway_env = {"PORT": "8080", "JWT_SECRET": self.secret,
                           "AUTH_SERVICE_URL": scheme + "://auth:3001", "CATALOG_SERVICE_URL": scheme + "://catalog:3002",
                           "INVENTORY_SERVICE_URL": scheme + "://inventory:3004"}
            gateway_mounts = []
            if tls:
                gateway_env["SSL_CERT_FILE"] = "/tls/ca.pem"
                gateway_mounts = [(self.work / "ca.pem", "/tls/ca.pem")]
            gateway = self.start("gateway-" + mode, self.images["api-gateway"], gateway_env, ports=(8080,), mounts=gateway_mounts)
            gateway_url = "http://127.0.0.1:" + str(self.port(gateway, 8080))
            endpoints = [(auth, 3001, "/health", "auth"), (catalog, 3002, "/health", "catalog"), (inventory, 3004, "/ready", "inventory")]
            for container, port, route, label in endpoints:
                self.stage = mode + "_readiness_" + label
                self.ready(scheme + "://127.0.0.1:" + str(self.port(container, port)) + route, context)
            self.stage = mode + "_readiness_gateway"
            self.ready(gateway_url + "/health")
            self.checks[mode + "_migration_before_startup_and_readiness"] = "PASS"
            self.stage = mode + "_gateway_roles_and_replay"
            tokens, identities = {}, {}
            password = "fixture_" + uuid.uuid4().hex
            self.private_values.append(password)
            for role in ("MANAGER", "STAFF", "CUSTOMER"):
                email = role.lower() + "-" + namespace + "@example.invalid"
                self.private_values.append(email)
                user = self.request(gateway_url + "/api/v1/auth/signup", "POST",
                                    {"email": email, "password": password, "display_name": "Owned NFR fixture"}, expected=201)
                self.sql("UPDATE " + auth_schema + ".users SET role='" + role + "' WHERE id='" + user["id"] + "'")
                session = self.request(gateway_url + "/api/v1/auth/login", "POST", {"email": email, "password": password})
                tokens[role], identities[role] = session["access_token"], user["id"]
                self.private_values.append(tokens[role])
            for i in range(5):
                self.request(gateway_url + "/api/v1/auth/login", "POST",
                             {"email": "wrong-" + str(i) + "-" + namespace + "@example.invalid", "password": password}, expected=401)
            self.request(gateway_url + "/api/v1/auth/login", "POST",
                         {"email": "manager-" + namespace + "@example.invalid", "password": password})
            catalog_url = gateway_url + "/api/v1/flavors"
            body = {"name": "Owned " + namespace, "description": "fixture", "price": {"amount_minor": 6000, "currency": "THB"},
                    "allergens": ["MILK"], "recipe": "fixture"}
            header = {"Idempotency-Key": str(uuid.uuid4())}
            flavor = self.request(catalog_url, "POST", body, tokens["MANAGER"], header, expected=201)
            if flavor != self.request(catalog_url, "POST", body, tokens["MANAGER"], header, expected=201): raise VerificationError("catalog_post_replay")
            self.request(catalog_url, "POST", body, tokens["STAFF"], expected=403)
            patch_key = {"Idempotency-Key": str(uuid.uuid4())}
            path = catalog_url + "/" + flavor["id"]
            changed = self.request(path, "PATCH", {"description": "first"}, tokens["MANAGER"], patch_key)
            self.request(path, "PATCH", {"description": "later"}, tokens["MANAGER"])
            if changed != self.request(path, "PATCH", {"description": "first"}, tokens["MANAGER"], patch_key): raise VerificationError("catalog_patch_replay")
            now = datetime.datetime.now(datetime.timezone.utc)
            create_body = {"flavor_id": flavor["id"], "production_date": now.strftime("%Y-%m-%d"),
                           "expires_at": (now + datetime.timedelta(hours=2)).isoformat(), "initial_portions": 10,
                           "unit_cost_minor": 125, "currency": "THB"}
            batch_url = gateway_url + "/api/v1/inventory/batches"
            header = {"Idempotency-Key": str(uuid.uuid4()), "X-User-ID": str(uuid.uuid4()), "X-User-Role": "CUSTOMER"}
            batch = self.request(batch_url, "POST", create_body, tokens["MANAGER"], header, expected=201)
            if batch != self.request(batch_url, "POST", create_body, tokens["MANAGER"], header, expected=201): raise VerificationError("production_replay")
            self.request(batch_url, "POST", create_body, tokens["STAFF"], expected=403)
            waste_body = {"portions": 1, "reason": "DAMAGED", "idempotency_key": str(uuid.uuid4())}
            waste_header = {"Idempotency-Key": str(uuid.uuid4())}
            waste_url = batch_url + "/" + batch["id"] + "/waste"
            waste = self.request(waste_url, "POST", waste_body, tokens["STAFF"], waste_header, expected=201)
            if waste != self.request(waste_url, "POST", waste_body, tokens["STAFF"], waste_header, expected=201): raise VerificationError("waste_replay")
            actor = self.sql("SELECT actor_id::text||':'||actor_role FROM " + inv_schema + ".inventory_movements WHERE batch_id='" + batch["id"] + "' AND kind='PRODUCED'")
            if actor != identities["MANAGER"] + ":MANAGER": raise VerificationError("spoofed_production_actor")
            self.checks[mode + "_actual_gateway_discovery_roles_lockout_and_rest_replay"] = "PASS"
            cg, ig = self.port(catalog, 50052), self.port(inventory, 50051)
            self.stage = mode + "_actual_order_clients"
            args = [str(self.work / "order-probe-bin"), "--catalog", "127.0.0.1:" + str(cg),
                    "--inventory", "127.0.0.1:" + str(ig), "--flavor", flavor["id"]]
            if tls: args.append("--expect-tls-rejection")
            probe_env = {**os.environ, "ORDER_SERVICE_TOKEN": self.machine, "GODEBUG": "",
                         "GRPC_GO_LOG_SEVERITY_LEVEL": "error", "GRPC_GO_LOG_VERBOSITY_LEVEL": "0"}
            result = json.loads(self.run(args, env=probe_env, timeout=20))
            if not result or not all(result.values()): raise VerificationError("actual_order_clients")
            self.checks[mode + "_actual_order_client_boundary"] = "PASS"
            if tls:
                self.stage = "tls_trust_and_machine_lifecycle"
                items = [{"flavor_id": flavor["id"], "portions": 1}]
                self.rpc("catalog.v1.CatalogService/GetFlavor", cg, {"flavor_id": flavor["id"]}, tls=True,
                         success=False, authority="wrong.invalid")
                self.rpc("catalog.v1.CatalogService/GetFlavor", cg, {"flavor_id": flavor["id"]}, tls=True,
                         success=False, ca="other-ca.pem")
                request = {"order_id": str(uuid.uuid4()), "idempotency_key": str(uuid.uuid4()), "items": items}
                self.rpc("inventory.v1.InventoryService/ReservePortions", ig, request, tokens["MANAGER"], tls=True, success=False)
                reservation = self.rpc("inventory.v1.InventoryService/ReservePortions", ig, request, self.machine, tls=True)["reservation"]
                self.rpc("inventory.v1.InventoryService/ConfirmReservation", ig,
                         {"order_id": request["order_id"], "reservation_id": reservation["reservationId"], "idempotency_key": str(uuid.uuid4())}, self.machine, tls=True)
                bad_gateway = self.start("bad-gateway", self.images["api-gateway"], gateway_env, ports=(8080,),
                                         mounts=[(self.work / "other-ca.pem", "/tls/ca.pem")])
                bad_url = "http://127.0.0.1:" + str(self.port(bad_gateway, 8080))
                self.ready(bad_url + "/health")
                try:
                    self.request(bad_url + "/api/v1/flavors")
                    raise VerificationError("gateway_accepted_untrusted_upstream")
                except VerificationError as error:
                    if not str(error).startswith("unexpected_http_status_5"): raise
                self.checks["tls_wrong_ca_hostname_and_user_machine_credentials_rejected"] = "PASS"
            self.stage = mode + "_stock_and_logs"
            balances = self.sql("SELECT initial_portions=available_portions+reserved_portions+sold_portions+wasted_portions AND available_portions=8 AND sold_portions=1 AND wasted_portions=1 FROM " + inv_schema + ".batches WHERE id='" + batch["id"] + "'")
            if balances != "t": raise VerificationError("stock_invariant_or_duplicate_mutation")
            for name in (auth, catalog, inventory):
                logs = self.docker("logs", name)
                if any(value in logs for value in self.private_values): raise VerificationError("covered_diagnostic_disclosure")
            self.checks[mode + "_stock_history_and_covered_diagnostics"] = "PASS"
        finally:
            for name in reversed(self.containers[start_index:]):
                logs = subprocess.run(["docker", "logs", name], text=True, stdout=subprocess.PIPE,
                                      stderr=subprocess.STDOUT, timeout=30).stdout
                (self.work / (name + "-runtime.log")).write_text(logs)
                subprocess.run(["docker", "rm", "-f", "-v", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
            del self.containers[start_index:]

    def close(self):
        for name in reversed(self.containers):
            subprocess.run(["docker", "rm", "-f", "-v", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        subprocess.run(["docker", "network", "rm", self.network], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        for image in self.images.values():
            subprocess.run(["docker", "image", "rm", image], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", type=Path, help="Write a credential-free JSON receipt")
    parser.add_argument("--diagnostics-dir", type=Path, help="Write redacted build/runtime logs; never keys or tokens")
    parser.add_argument("--mode", choices=("plain", "tls", "all"), default="all")
    args = parser.parse_args()
    for tool in ("docker", "go", "openssl", "grpcurl"):
        if not shutil.which(tool):
            parser.error("required executable missing: " + tool)
    root = Path(__file__).resolve().parents[2]
    report = {"checks": {}, "status": "failed", "limitations": [
        "No shared environment or database is migrated.",
        "The existing Order clients retain plaintext defaults; their TLS adoption requires their owner.",
        "Gateway inbound HTTPS, full Order/Payment checkout and production deployment remain separate acceptance work.",
    ]}
    with tempfile.TemporaryDirectory(prefix="gelato-owned-nfr-") as temporary:
        os.chmod(temporary, 0o700)
        fixture = Fixture(root, Path(temporary))
        try:
            fixture.certificates()
            fixture.build()
            fixture.prepare()
            for mode in ((False, True) if args.mode == "all" else (args.mode == "tls",)):
                fixture.profile(mode)
            report.update(status="passed", checks=fixture.checks)
        except Exception as error:
            report.update(stage=fixture.stage, error_type=type(error).__name__, checks=fixture.checks)
            if isinstance(error, VerificationError):
                report["error_code"] = str(error)
        finally:
            fixture.close()
            if args.diagnostics_dir:
                args.diagnostics_dir.mkdir(parents=True, exist_ok=True)
                for source in fixture.work.glob("*.log"):
                    text = source.read_text()
                    for value in fixture.private_values:
                        text = text.replace(value, "[REDACTED]")
                    (args.diagnostics_dir / source.name).write_text(text)
    encoded = json.dumps(report, indent=2) + "\n"
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(encoded)
    print(encoded, end="")
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    def interrupted(*_):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupted)
    raise SystemExit(main())
