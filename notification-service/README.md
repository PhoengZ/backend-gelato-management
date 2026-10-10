# Notification Service

แจ้งเตือนแบบ in-app ของ GelatoFlow — รับ event จาก RabbitMQ แล้ว push popup ไปยังเบราว์เซอร์ที่เชื่อมต่ออยู่ผ่าน Server-Sent Events (SSE) ไม่มี SMS/email/push provider ภายนอก

Contract อยู่ที่ [`contracts/openapi/notification-service.v1.yaml`](../contracts/openapi/notification-service.v1.yaml) และ `contracts/events/*.schema.json`
Stack: Go, stdlib `net/http`, amqp091-go (RabbitMQ), golang-jwt (HS256)

Service นี้ **stateless**: ไม่มี database ไม่ถือข้อมูล business ใด ๆ (ดู `docs/architecture/service-data-ownership.md`)

## สถานะการพัฒนา

| ส่วน | สถานะ |
| --- | --- |
| `GET /api/v1/notifications/stream` (SSE, Bearer JWT, keep-alive) | ทำแล้ว (unit/httptest ผ่าน) |
| Consumer `order.placed`, `order.cancelled`, `fulfillment.order_ready`, `inventory.low_stock`, `inventory.expiring` + dedupe ด้วย event `id` (in-memory) | ทำแล้ว (unit test ผ่าน) |
| RabbitMQ user `notification_service` + permission ใน `infra/rabbitmq/definitions.json`, compose, `.env.example` | เพิ่มแล้ว |
| ทดสอบกับ RabbitMQ จริงและ publisher จริงของ Order/Fulfillment | ยังไม่ทำ (ดูหัวข้อ "ทดลองกับ broker") |
| Route ใน API Gateway | ยังไม่ทำ — Gateway ใช้ `proxy.Do` ซึ่งไม่รองรับ stream ต้องแก้และให้เจ้าของ Gateway รีวิว |
| Publisher ของ `inventory.low_stock` / `inventory.expiring` | ยังไม่มีฝั่ง Inventory และยังไม่มี JSON schema (service รับแบบ tolerant ตาม `docs/event-schema-spec.md` §3.4) |
| DLQ | ไม่มีทั้ง repo; message ที่ผิดรูปจะถูก `Nack(requeue=false)` |
| Persistence / replay / WebSocket | นอกขอบเขต |

## ใครได้รับอะไร

| Event | Notification `type` | ผู้รับ |
| --- | --- | --- |
| `order.placed` | `order.confirmed` | ลูกค้าเจ้าของ order (`customer_id`) |
| `fulfillment.order_ready` | `order.ready` (มี `queue_number`) | ลูกค้าเจ้าของ order |
| `order.cancelled` | `order.cancelled` (มี `reason`) | ลูกค้าเจ้าของ order |
| `inventory.low_stock` | `inventory.low_stock` | STAFF, MANAGER |
| `inventory.expiring` | `inventory.expiring` | STAFF, MANAGER |

## ข้อจำกัดที่ต้องรู้

- **ไม่มี queue number ใน confirmation**: `order.placed` ไม่มี queue number (Fulfillment เป็นคนออกให้ภายหลัง) ลูกค้าจะเห็นเลขคิวใน notification `order.ready` แทน
- **การหาว่า order เป็นของใคร**: `order.ready` และ `order.cancelled` ไม่มี `customer_id` service จึงจำ `order_id → customer_id` จาก `order.placed` ไว้ใน memory (หมดอายุตาม `ORDER_OWNER_TTL`, จำกัดจำนวน) ถ้า service restart หรือ order เก่ากว่า TTL จะไม่ส่ง popup (log `no known owner`) ทางแก้ถาวรคือเรียก Order gRPC `GetOrderDetails` หรือเพิ่ม `customer_id` ใน thin event — ต้องคุยกับเจ้าของ Order
- **ส่งแบบ best effort**: ผู้ใช้ที่ไม่ได้เชื่อมต่ออยู่ตอนนั้นจะไม่ได้รับ และไม่มี replay ตอน reconnect ฝั่ง frontend ควรยัง poll `GET /api/v1/orders/{id}/status` เป็น fallback
- **Dedupe**: เก็บ event id ล่าสุดใน memory เท่านั้น หลัง restart อาจเห็น popup ซ้ำได้หนึ่งครั้ง
- `EventSource` ของเบราว์เซอร์ส่ง `Authorization` header ไม่ได้ ให้ใช้ fetch-based SSE (เช่น `@microsoft/fetch-event-source`)

## Endpoints

| Method | Path | หมายเหตุ |
| --- | --- | --- |
| GET | `/api/v1/notifications/stream` | ต้องมี `Authorization: Bearer <jwt>` (CUSTOMER / STAFF / MANAGER); ไม่ผ่าน → 401 `UNAUTHORIZED` |
| GET | `/health` | `{"status":"ok"}` ไม่ได้เช็ค broker |

แต่ละ frame:

```text
id: 5b0c1f1e-3a52-4d8c-9a41-0d6f4f0f7a10
event: order.ready
data: {"id":"5b0c1f1e-...","type":"order.ready","message":"Your gelato order is ready for pickup!","occurred_at":"2026-09-02T14:55:00Z","data":{"order_id":"...","queue_number":"01","pickup_slot_id":"..."}}
```

บรรทัดที่ขึ้นต้นด้วย `:` (`: connected`, `: keep-alive`) เป็น comment ให้ client ข้ามได้

## Configuration

| Env | Default | หมายเหตุ |
| --- | --- | --- |
| `PORT` | `8080` | |
| `JWT_SECRET` | — (บังคับ ≥ 32 bytes) | ต้องเป็น secret เดียวกับที่ Auth ใช้เซ็น |
| `JWT_ISSUER` / `JWT_AUDIENCE` | `gelatoflow-auth` / `gelatoflow-api` | |
| `RABBITMQ_URL` | ว่าง | ว่าง = ไม่ใช้ broker (SSE ยังทำงาน แต่ไม่มีอะไรถูก push) |
| `ORDER_OWNER_TTL` | `24h` | |
| `HEARTBEAT_INTERVAL` | `25s` | |

## รันในเครื่อง

ผ่าน compose (จาก repo root):

```bash
cp infra/.env.example infra/.env   # แล้วตั้ง NOTIFICATION_JWT_SECRET ให้ตรงกับ Auth
docker compose --env-file infra/.env \
  -f infra/docker/compose.yml -f infra/docker/compose.dev.yml up --build rabbitmq notification-service
# service: http://localhost:8082
```

หรือรันตรง ๆ: `cp .env.example .env` แล้ว `go run ./cmd/api`

Topology ถูกประกาศโดย service ตอนเริ่ม: exchange `order`, `fulfillment`, `inventory` (topic, durable) และ queue `notification_queue` bind กับ `order.placed`, `order.cancelled`, `fulfillment.order_ready`, `inventory.low_stock`, `inventory.expiring`

## ทดลองกับ broker

หลังรัน compose แล้ว สร้าง JWT (HS256, claims `sub`=UUID, `role`, `iss`, `aud`, `iat`, `exp`) เปิด stream แล้ว publish ตัวอย่างจาก `contracts/examples/` ไปที่ exchange `order` / `fulfillment` ผ่าน Management UI (`http://localhost:15672`) แล้วดู frame ที่ได้รับ:

```bash
curl -N -H "Authorization: Bearer $TOKEN" http://localhost:8082/api/v1/notifications/stream
```

## ทดสอบ

```bash
gofmt -s -l .
go vet ./...
go test -race -count=1 ./...
```

## โครงสร้าง

```
cmd/api             bootstrap: config, hub, broker consumer, http server, graceful shutdown
internal/config     env + validation
internal/events     CloudEvents envelope และ payload ที่รับ
internal/auth       ตรวจ JWT (รูปแบบเดียวกับ batch-inventory-service)
internal/hub        registry ของ SSE connection, PushToUser / PushToRoles (= pushInAppNotification)
internal/notify     event -> notification, order owner map, dedupe
internal/messaging  RabbitMQ consumer และ Dispatch
internal/handler    /health และ SSE stream
```
