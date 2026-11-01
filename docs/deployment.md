# Deployment: single host with Docker Compose

This guide puts the whole system (all backend services, their databases, RabbitMQ,
the Next.js frontend, and HTTPS) on one Linux VM. It targets a free Oracle Cloud
Always Free Ampere A1 instance, but any Ubuntu host with Docker works. It is sized
for a demo or project presentation with light traffic. It does not provide high
availability.

```text
Internet ──HTTPS──▶ Caddy :443 ─┬─ /api/v1/payments/webhook ─▶ api-gateway   (Stripe)
                                └─ everything else ───────────▶ frontend:3000 (Next.js)
frontend ──/api/v1/* + Bearer token──▶ api-gateway ──▶ services ──▶ databases / RabbitMQ
```

Only Caddy publishes ports. Nothing else is reachable from the internet.

## Files

| File | Purpose |
| --- | --- |
| `infra/docker/compose.prod.yml` | Production overlay: Caddy, frontend, Stripe and CORS settings, production broker users |
| `infra/caddy/Caddyfile` | TLS termination and routing |
| `infra/scripts/generate_prod_env.py` | Creates `infra/.env.prod` and `infra/rabbitmq/definitions.prod.json` with random secrets |
| `infra/scripts/promote_user.sh` | Promotes a signed-up account to STAFF or MANAGER |

Do not use `compose.dev.yml` on a server. It publishes database and service ports.

## 1. Create the VM

1. Sign up for Oracle Cloud. A card is needed for verification. Choose the home
   region carefully because it cannot be changed later.
2. Create an instance with shape **VM.Standard.A1.Flex** and image **Ubuntu 24.04**.
   Stay inside the current Always Free allowance shown in the console. Recent reports
   put it at about 2 OCPU and 12 GB in total. This stack idles at roughly 3–4 GB.
   If the console says "Out of capacity", retry later or pick another availability
   domain.
3. In the VCN security list, add ingress rules for TCP 80 and TCP 443, plus UDP 443
   for HTTP/3. Ubuntu images on OCI also have iptables rules, so open the ports there too:

   ```bash
   sudo iptables -I INPUT 6 -p tcp -m multiport --dports 80,443 -j ACCEPT
   sudo iptables -I INPUT 6 -p udp --dport 443 -j ACCEPT
   sudo netfilter-persistent save
   ```

## 2. Get a host name

Any DNS name that points at the VM's public IP works. A free option is
[DuckDNS](https://www.duckdns.org): create `yourname.duckdns.org` and set its IP to
the VM's public IP. Caddy requests the Let's Encrypt certificate itself.

## 3. Install Docker and fetch both repositories

```bash
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER" && newgrp docker

# Cap container logs so a long-running demo cannot fill the disk.
echo '{"log-driver":"json-file","log-opts":{"max-size":"10m","max-file":"3"}}' \
  | sudo tee /etc/docker/daemon.json && sudo systemctl restart docker

mkdir -p ~/gelatoflow && cd ~/gelatoflow
git clone <backend-repo-url>  backend-gelato-management
git clone <frontend-repo-url> frontend-gelato-management
```

The overlay builds the frontend from `../../../frontend-gelato-management` relative to
`infra/docker`. Set `FRONTEND_CONTEXT` in `.env.prod` if you clone it somewhere else.
All images build natively on ARM64.

## 4. Generate secrets

```bash
cd ~/gelatoflow/backend-gelato-management
python3 infra/scripts/generate_prod_env.py --domain yourname.duckdns.org --email you@example.com
```

Then edit `infra/.env.prod` and fill in the Stripe test-mode values:

- `STRIPE_SECRET_KEY`: Stripe Dashboard → Developers → API keys (`sk_test_…`)
- `STRIPE_WEBHOOK_SECRET`: Developers → Webhooks → add endpoint
  `https://yourname.duckdns.org/api/v1/payments/webhook`, then copy its signing
  secret (`whsec_…`). Subscribe to the events listed in `payment-service/WEBHOOK.md`.

Keep `STRIPE_LIVE_MODE=false`. Test mode costs nothing and accepts card
`4242 4242 4242 4242`.

Never commit `.env.prod` or `definitions.prod.json`. Both are git-ignored. Back them
up privately. Regenerating them with `--force` after the first start changes
passwords that the existing database volumes were initialised with.

## 5. Start

```bash
docker compose --env-file infra/.env.prod \
  -f infra/docker/compose.yml -f infra/docker/compose.prod.yml up -d --build
docker compose --env-file infra/.env.prod \
  -f infra/docker/compose.yml -f infra/docker/compose.prod.yml ps
```

The first build takes several minutes. Open `https://yourname.duckdns.org`.

## 6. Create staff and manager accounts

Signup creates only CUSTOMER accounts. Sign up through the website, then promote:

```bash
infra/scripts/promote_user.sh manager@example.com MANAGER
infra/scripts/promote_user.sh staff@example.com STAFF
```

The user must log out and in again to receive a token with the new role.

## Updating

```bash
git -C ~/gelatoflow/backend-gelato-management pull
git -C ~/gelatoflow/frontend-gelato-management pull
docker compose --env-file infra/.env.prod \
  -f infra/docker/compose.yml -f infra/docker/compose.prod.yml up -d --build
```

## Backups

Volumes hold all data. Dump the databases before demos and before upgrades:

```bash
mkdir -p ~/backups && cd ~/backups
for db in auth inventory fulfillment order; do
  docker exec "$db-postgres" sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' > "$db-$(date +%F).sql"
done
docker exec analytics-mongodb sh -c \
  'mongodump --quiet --archive -u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin' \
  > "analytics-$(date +%F).archive"
```

## Known limitations

- **Rate limits are shared.** All browser traffic reaches the Gateway from the
  frontend container, so the per-IP limit on login and signup (100 requests/min)
  applies to the whole site rather than to each visitor.
- **Order exposes no gRPC server.** Payment's Order gRPC client and Analytics'
  cancellation enrichment point at `order-service:50054`, but nothing listens there.
  Calls on those paths fail until Order serves gRPC.
- **Order status stops at `PAID`.** Fulfillment status is not fed back into Order.
  The frontend shows readiness from the `order.ready` notification instead.
- **Fulfillment migrations** run from `docker-entrypoint-initdb.d`, which runs only
  when the volume is first created. Apply later migrations manually.
- **Single host.** A VM failure takes the whole system down. Oracle may reclaim
  Always Free instances that stay idle, so keep recent backups.
