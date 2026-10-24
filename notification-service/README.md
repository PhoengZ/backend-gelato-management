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
| Reconnect RabbitMQ อัตโนมัติ (backoff 1s → 30s) และ `/health` ตอบ 503 เมื่อ broker หลุด | ทำแล้ว (unit test ผ่าน) |
| ปิด SSE stream เมื่อ JWT หมดอายุ (ส่ง `: token-expired` แล้วปิด) | ทำแล้ว (unit test ผ่าน) |
| Replay ผ่าน `Last-Event-ID` จาก buffer ใน memory (500 รายการต่อ user/role, เก็บ 5 นาที, ล้างทุก 1 นาที) รวม alert ของ STAFF/MANAGER | ทำแล้ว (unit test ผ่าน) |
| DLQ: `notification.dlx` → `notification_dlq`, message ที่ผิดรูปถูก `Nack(requeue=false)` | ทำแล้ว (unit test ผ่าน) |
| Route ใน API Gateway (`GET /api/v1/notifications/stream` ผ่าน `api-gateway/internal/proxy/sse.go`) | ทำแล้ว |
| ทดสอบกับ RabbitMQ จริงและ publisher จริงของ Order/Fulfillment | ดู `tests/test_uc01_preorder_checkout.py` และหัวข้อ "ทดลองกับ broker" |
| Publisher ของ `inventory.low_stock` / `inventory.expiring` | Inventory publish ผ่าน outbox แล้ว schema อยู่ที่ `contracts/events/inventory-low-stock.v1.schema.json` และ `inventory-expiring.v1.schema.json` (ทดสอบ E2E แล้ว) |
| Persistence / WebSocket | นอกขอบเขต |

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
- **ส่งแบบ best effort**: ถ้า reconnect ภายใน 5 นาทีพร้อม `Last-Event-ID` จะได้ notification ที่พลาดไป แต่ถ้านานกว่านั้นหรือ service restart จะหาย ฝั่ง frontend ควรยัง poll `GET /api/v1/orders/{id}/status` เป็น fallback
- **รันได้ 1 replica เท่านั้น**: ทุก instance ใช้ `notification_queue` ร่วมกัน (competing consumers) event หนึ่งจะไปถึงแค่ instance เดียว และ order owner map กับ replay buffer อยู่ใน memory ของแต่ละ process ถ้าจะ scale ต้องเปลี่ยนเป็น queue ต่อ instance ก่อน
- **Stream ปิดเมื่อ token หมดอายุ**: client ต้องต่อใหม่ด้วย token ใหม่ และส่ง `Last-Event-ID` เพื่อรับสิ่งที่พลาด
- **Dedupe**: เก็บ event id ล่าสุดใน memory เท่านั้น หลัง restart อาจเห็น popup ซ้ำได้หนึ่งครั้ง
- `EventSource` ของเบราว์เซอร์ส่ง `Authorization` header ไม่ได้ ให้ใช้ fetch-based SSE (เช่น `@microsoft/fetch-event-source`)

## Endpoints

| Method | Path | หมายเหตุ |
| --- | --- | --- |
| GET | `/api/v1/notifications/stream` | ต้องมี `Authorization: Bearer <jwt>` (CUSTOMER / STAFF / MANAGER); ไม่ผ่าน → 401 `UNAUTHORIZED` |
| GET | `/health` | `200 {"status":"ok"}`; ถ้าตั้ง `RABBITMQ_URL` แต่ broker ยังไม่เชื่อมต่อ → `503 {"status":"degraded","broker":"disconnected"}` |

แต่ละ frame:

```text
id: 5b0c1f1e-3a52-4d8c-9a41-0d6f4f0f7a10
event: order.ready
data: {"id":"5b0c1f1e-...","type":"order.ready","message":"Your gelato order is ready for pickup!","occurred_at":"2026-09-02T14:55:00Z","data":{"order_id":"...","queue_number":"01","pickup_slot_id":"..."}}
```

บรรทัดที่ขึ้นต้นด้วย `:` (`: connected`, `: keep-alive`, `: token-expired`) เป็น comment ให้ client ข้ามได้

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
internal/messaging  RabbitMQ consumer, Dispatch และ reconnect loop (Run)
internal/stream     replay buffer สำหรับ Last-Event-ID
internal/handler    /health และ SSE stream
```
