# Fulfillment Service

คิวครัวและสถานะการรับสินค้าของ GelatoFlow — สร้าง ticket จาก event `order.placed`, ให้พนักงานเปลี่ยนสถานะ, แล้วส่ง event `fulfillment.order_ready` / `fulfillment.order_picked_up`

Contract อยู่ที่ [`contracts/openapi/fulfillment-service.v1.yaml`](../contracts/openapi/fulfillment-service.v1.yaml) และ `contracts/events/fulfillment-*.schema.json`
Stack: Go, Echo, pgx (PostgreSQL), amqp091-go (RabbitMQ, optional)

## สถานะการพัฒนา

| ส่วน | สถานะ |
| --- | --- |
| REST: list / get / `PATCH .../status`, state machine, error แบบ `code/message/details` | ทำแล้ว (unit test ผ่าน) |
| Consumer `order.placed` / `order.cancelled` + dedupe ด้วย event `id` | ทำแล้ว (unit test ผ่าน; ยังไม่ได้ทดสอบกับ broker จริง) |
| Queue number ต่อ pickup slot (slot 15 นาที สร้างอัตโนมัติจาก `pickup_at`) | ทำแล้ว (SQL ยังไม่ได้ทดสอบกับ Postgres จริง) |
| Publisher CloudEvents ผ่าน transactional outbox → exchange `fulfillment` | ทำแล้ว (ยังไม่ได้ทดสอบกับ broker จริง) |
| Pickup slot management (GET `/api/v1/fulfillments/slots/capacity?slot_time=...` ดู active ticket count ใน slot 15 นาที) | ทำแล้ว (unit test ผ่าน) |
| QR pickup | นอกขอบเขตตอนนี้ |
| Local JWT verification & RBAC middleware (`STAFF`, `MANAGER` สำหรับ mutating endpoints) | ทำแล้ว (unit test ผ่าน) |
| RabbitMQ DLX `fulfillment.dlx` + DLQ `fulfillment_queue_dlq` (reject poison messages ด้วย Nack requeue=false) | ทำแล้ว (unit test ผ่าน) |
| RabbitMQ user `fulfillment_service` + permission ใน `infra/rabbitmq/definitions.json` | เพิ่มแล้ว (ยังไม่ได้ทดสอบกับ broker จริง; ควรให้เจ้าของ service อื่นรีวิวตอน PR) |

## Endpoints

| Method | Path | หมายเหตุ |
| --- | --- | --- |
| GET | `/api/v1/fulfillments?status=PREPARING,READY_FOR_PICKUP` | เรียงตาม slot แล้วตาม queue |
| GET | `/api/v1/fulfillments/slots/capacity?slot_time=...` | ดู active ticket count ใน slot window 15 นาที |
| GET | `/api/v1/fulfillments/{order_id}` | |
| PATCH | `/api/v1/fulfillments/{order_id}/status` | ต้องใช้บทบาท `STAFF` หรือ `MANAGER`; body `{"status":"READY_FOR_PICKUP"}` หรือ `PICKED_UP`; ข้ามขั้น → 409 `INVALID_TRANSITION` |
| GET | `/health` | |

Ticket ไม่ถูกสร้างผ่าน REST — สร้างจาก event `order.placed` เท่านั้น และ `order.cancelled` จะเปลี่ยน ticket ที่ยัง `PREPARING` เป็น `CANCELLED`

## รันในเครื่อง

ผ่าน compose (จาก repo root):

```bash
cp infra/.env.example infra/.env
docker compose --env-file infra/.env \
  -f infra/docker/compose.yml -f infra/docker/compose.dev.yml up --build fulfillment-service
# service: http://localhost:8081   postgres: localhost:5435
```

Migration ใน `migrations/` ถูก mount เข้า Postgres และรันครั้งแรกที่สร้าง volume (001 สร้างตารางเดิมของ assignment แล้ว 002 แทนที่ด้วย schema ปัจจุบัน)

หรือรันตรง ๆ: `cp .env.example .env` แล้ว `go run ./cmd/api` (ต้องมี Postgres ที่รัน migration แล้ว)
`RABBITMQ_URL` ว่าง = ไม่ใช้ broker; API ยังทำงานและ event จะค้างใน outbox จนกว่าจะเชื่อม broker

## ทดสอบ

```bash
gofmt -l .
go vet ./...
go test ./...
```

## โครงสร้าง

```
cmd/api            bootstrap: config, pgx pool, broker, relay, consumer, echo
internal/models    Fulfillment, สถานะ และ state machine
internal/events    CloudEvents envelope + payload ของ event ที่รับ/ส่ง
internal/repository SQL: ticket, slot/queue, dedupe, outbox (ทุกการเปลี่ยนสถานะอยู่ใน transaction เดียว)
internal/handler   Echo handler
internal/messaging broker, outbox relay, consumer
migrations         schema
```
