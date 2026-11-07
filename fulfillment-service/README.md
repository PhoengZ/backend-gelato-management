# Fulfillment Service

คิวครัวและสถานะการรับสินค้าของ GelatoFlow — สร้าง ticket (`QUEUED`) จาก event `order.placed` แล้วส่ง `fulfillment.order_queued` พร้อมเลขคิว, ให้พนักงานเปลี่ยนสถานะ, แล้วส่ง event `fulfillment.order_ready` / `fulfillment.order_picked_up`

Lifecycle: `QUEUED` → `PREPARING` → `READY_FOR_PICKUP` → `PICKED_UP`; `order.cancelled` เปลี่ยน ticket ที่ยัง `QUEUED` หรือ `PREPARING` เป็น `CANCELLED`

Contract อยู่ที่ [`contracts/openapi/fulfillment-service.v1.yaml`](../contracts/openapi/fulfillment-service.v1.yaml) และ `contracts/events/fulfillment-*.schema.json`
Stack: Go, Echo, pgx (PostgreSQL), amqp091-go (RabbitMQ, optional)

## สถานะการพัฒนา

| ส่วน | สถานะ |
| --- | --- |
| REST: list / get / `PATCH .../status`, state machine, error แบบ `code/message/details` | ทำแล้ว (unit test ผ่าน) |
| Consumer `order.placed` / `order.cancelled` + dedupe ด้วย event `id` | ทำแล้ว (unit test ผ่าน; ยังไม่ได้ทดสอบกับ broker จริง) |
| Queue number ต่อ pickup slot (slot 15 นาที สร้างอัตโนมัติจาก `pickup_at`) | ทำแล้ว (integration test กับ Postgres จริงผ่าน) |
| กัน event สลับลำดับ: `order.cancelled` ที่มาก่อน `order.placed` จะเก็บ tombstone ใน `cancelled_orders` แล้ว `order.placed` ที่มาทีหลังจะไม่สร้าง ticket | ทำแล้ว (integration test ผ่าน) |
| Retention: ลบ outbox ที่ publish แล้วเกิน 7 วัน และ `processed_events` / tombstone เกิน 30 วัน ทุก 1 ชั่วโมง | ทำแล้ว (integration test ผ่าน) |
| Audit: history เก็บ `actor_id` (staff) หรือ `source_event_id` (event) และอ่านได้ที่ `GET .../{order_id}/history` (MANAGER) | ทำแล้ว (unit + integration test ผ่าน) |
| ลูกค้าดู ticket ของตัวเองได้ (เก็บ `customer_id` จาก `order.placed`; ticket ของคนอื่นหรือ ticket เก่าที่ไม่มี `customer_id` ได้ 404) | ทำแล้ว (unit + integration test ผ่าน) |
| Observability: `X-Request-ID` (ใช้ค่าจาก Gateway ถ้ามี) อยู่ใน access log; `/health` เช็คทั้ง DB และ broker; compose healthcheck | ทำแล้ว (unit test ผ่าน) |
| HTTP server timeouts (read header 5s, read 10s, write 15s, idle 60s) | ทำแล้ว |
| Publisher CloudEvents ผ่าน transactional outbox → exchange `fulfillment` | ทำแล้ว (ยังไม่ได้ทดสอบกับ broker จริง) |
| Pickup slot management (GET `/api/v1/fulfillments/slots/capacity?slot_time=...` ดู active ticket count ใน slot 15 นาที) | ทำแล้ว (unit test ผ่าน) |
| QR pickup | นอกขอบเขตตอนนี้ |
| Local JWT verification & RBAC middleware (`STAFF`, `MANAGER` สำหรับ mutating endpoints) | ทำแล้ว (unit test ผ่าน); `JWT_SECRET` ต้องตั้งและยาว ≥ 32 bytes ไม่งั้น service ไม่ start |
| RabbitMQ DLX `fulfillment.dlx` + DLQ `fulfillment_queue_dlq` (reject poison messages ด้วย Nack requeue=false) | ทำแล้ว (unit test ผ่าน) |
| Retry แบบมี backoff ผ่าน `fulfillment_queue_retry` (TTL ต่อ message แล้วเด้งกลับ `fulfillment_queue`): DB ต่อไม่ติด/ไม่พร้อม retry ไม่จำกัด (1s, 2s, 4s … สูงสุด 1 นาที) เพื่อไม่ให้ order ที่จ่ายแล้วตกไป DLQ; storage error อื่นครบ 5 ครั้ง (1s ถึง 16s) แล้วเข้า DLQ | ทำแล้ว (unit test ผ่าน; ยังไม่ได้ทดสอบกับ broker จริง) |
| Reconnect broker อัตโนมัติ (consumer + outbox relay, backoff 1s ถึง 30s); `/health` ตอบ 503 ระหว่าง broker หลุด | ทำแล้ว (unit test ผ่าน; ยังไม่ได้ทดสอบกับ broker จริง) |
| RabbitMQ user `fulfillment_service` + permission ใน `infra/rabbitmq/definitions.json` | เพิ่มแล้ว (ยังไม่ได้ทดสอบกับ broker จริง; ควรให้เจ้าของ service อื่นรีวิวตอน PR) |

## Endpoints

| Method | Path | หมายเหตุ |
| --- | --- | --- |
| GET | `/api/v1/fulfillments?status=QUEUED,PREPARING,READY_FOR_PICKUP&pickup_from=...&pickup_to=...` | `STAFF`/`MANAGER`; เรียงตาม slot แล้วตาม queue; `pickup_from`/`pickup_to` (RFC3339, ช่วง `[from, to)`) เป็น optional ถ้าไม่ส่งจะได้ทุก ticket เหมือนเดิม |
| GET | `/api/v1/fulfillments/slots/capacity?slot_time=...` | ดู active ticket count ใน slot window 15 นาที |
| GET | `/api/v1/fulfillments/{order_id}` | `STAFF`/`MANAGER` เห็นทุก ticket; `CUSTOMER` เห็นเฉพาะของ order ตัวเอง (ไม่ใช่ของตัวเอง → 404) |
| GET | `/api/v1/fulfillments/{order_id}/history` | `MANAGER`; audit trail เรียงจากเก่าไปใหม่ |
| PATCH | `/api/v1/fulfillments/{order_id}/status` | ต้องใช้บทบาท `STAFF` หรือ `MANAGER`; body `{"status":"READY_FOR_PICKUP"}` หรือ `PICKED_UP`; ข้ามขั้น → 409 `INVALID_TRANSITION` |
| GET | `/health` | 503 `{"status":"degraded","database":"unreachable"}` เมื่อ DB ping ไม่ผ่าน (timeout 2s) หรือ `{"status":"degraded","broker":"disconnected"}` เมื่อตั้ง `RABBITMQ_URL` แต่ broker หลุด |

`PATCH .../status` รับ `PREPARING` (จาก `QUEUED`, ไม่ส่ง event), `READY_FOR_PICKUP` (จาก `PREPARING`, ส่ง `fulfillment.order_ready`) และ `PICKED_UP` (จาก `READY_FOR_PICKUP`, ส่ง `fulfillment.order_picked_up`); ส่งสถานะเดิมซ้ำ = no-op

Ticket ไม่ถูกสร้างผ่าน REST — สร้างจาก event `order.placed` เท่านั้น (สถานะเริ่มต้น `QUEUED`) และเขียน `fulfillment.order_queued` ลง outbox ใน transaction เดียวกับ ticket; `order.cancelled` จะเปลี่ยน ticket ที่ยัง `QUEUED` หรือ `PREPARING` เป็น `CANCELLED`

`fulfillment.order_queued` บังคับ `customer_id` ถ้า `order.placed` ไม่มี `customer_id` (producer รุ่นเก่า) ticket ยังถูกสร้างแต่จะไม่ส่ง event นี้ (log ไว้) เพราะไม่มีลูกค้าให้แจ้ง; ticket เก่าที่ `customer_id` เป็น NULL ไม่กระทบ เพราะ event นี้ส่งเฉพาะตอนสร้าง ticket

## รันในเครื่อง

ผ่าน compose (จาก repo root):

```bash
cp infra/.env.example infra/.env
docker compose --env-file infra/.env \
  -f infra/docker/compose.yml -f infra/docker/compose.dev.yml up --build fulfillment-service
# service: http://localhost:8081   postgres: localhost:5435
```

Migration ใน `migrations/` ถูก mount เข้า Postgres และรันครั้งแรกที่สร้าง volume (001 สร้างตารางเดิมของ assignment แล้ว 002 แทนที่ด้วย schema ปัจจุบัน) และ service รันทุกไฟล์ที่ยังไม่อยู่ใน `fulfillment_schema_migrations` ตอน start (`migrations.Apply`) ทุกไฟล์จึงต้องรันซ้ำได้
005 เพิ่ม `QUEUED` ใน CHECK constraint ของ `fulfillments` และ `fulfillment_status_history` และเปลี่ยน default เป็น `QUEUED` โดยไม่แก้แถวเดิม (ticket ที่ `PREPARING` อยู่แล้วยังเป็น `PREPARING`) ใช้กับ database production ที่มีข้อมูลได้ตอน service start

หรือรันตรง ๆ: `cp .env.example .env` แล้ว `go run ./cmd/api` (ต้องมี Postgres ที่รัน migration แล้ว)
`RABBITMQ_URL` ว่าง = ไม่ใช้ broker; API ยังทำงานและ event จะค้างใน outbox จนกว่าจะเชื่อม broker
ถ้าตั้ง `RABBITMQ_URL` แล้ว broker ยังไม่พร้อมหรือหลุดกลางทาง service จะต่อใหม่เองโดยไม่ต้อง restart
`JWT_SECRET` ไม่มีค่า default แล้ว ต้องใช้ค่าเดียวกับ Auth/Gateway (compose ส่งให้จาก `infra/.env`)

## ทดสอบ

```bash
gofmt -l .
go vet ./...
go test ./...

# integration test ของ repository (ต้องมี Postgres ว่าง ๆ; test จะ TRUNCATE ตาราง)
docker run -d --rm --name fulfillment-it-pg -e POSTGRES_PASSWORD=test -p 55432:5432 postgres:16-alpine
FULFILLMENT_TEST_DATABASE_URL=postgres://postgres:test@localhost:55432/postgres?sslmode=disable \
  go test -p 1 -tags integration ./internal/repository/... ./migrations/...
docker stop fulfillment-it-pg
```

## โครงสร้าง

```
cmd/api            bootstrap: config, pgx pool, broker, relay, consumer, echo
internal/models    Fulfillment, สถานะ และ state machine
internal/events    CloudEvents envelope + payload ของ event ที่รับ/ส่ง
internal/repository SQL: ticket, slot/queue, dedupe, outbox (ทุกการเปลี่ยนสถานะอยู่ใน transaction เดียว)
internal/handler   Echo handler
internal/messaging broker, outbox relay, consumer, retry queue, reconnect loop (run.go)
migrations         schema
```
