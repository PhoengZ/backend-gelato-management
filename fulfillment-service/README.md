# Kitchen Ticket Service

REST API สำหรับ resource `kitchen_ticket` (ส่วนหนึ่งของ Fulfillment Service ในโปรเจกต์ GelatoFlow)
ทำขึ้นสำหรับ assignment งานเดี่ยวเรื่อง REST API — ใช้ Go + Echo + pgx (PostgreSQL) + amqp091-go (RabbitMQ, optional)

## 1. เตรียม PostgreSQL

ใช้ Docker เร็วสุด:

```bash
docker run --name fulfillment-pg -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=fulfillment -p 5432:5432 -d postgres:16
```

รัน migration (สร้างตาราง + seed ข้อมูลตัวอย่าง):

```bash
docker exec -i fulfillment-pg psql -U postgres -d fulfillment < migrations/001_create_kitchen_tickets.sql
```

## 2. ตั้งค่า .env

```bash
cp .env.example .env
```

ถ้าไม่อยากรัน RabbitMQ ระหว่าง demo ก็ปล่อย `RABBITMQ_URL` ว่างไว้ได้เลย — โค้ดจะรันต่อโดยไม่ publish event
(เช็คได้จาก log ตอน start: `RABBITMQ_URL not set, running without a message broker`)

ถ้าอยากโชว์ event ด้วย รัน RabbitMQ:

```bash
docker run --name fulfillment-rabbit -p 5672:5672 -p 15672:15672 -d rabbitmq:3-management
```

## 3. ติดตั้ง dependency แล้วรัน

```bash
go mod tidy
go run ./cmd/api
```

เซิร์ฟเวอร์จะขึ้นที่ `http://localhost:8080`

## 4. ทดสอบ / ใช้ demo วิดีโอ

**GET ALL**
```bash
curl http://localhost:8080/api/v1/tickets
```

**POST (สร้างตั๋วใหม่)**
```bash
curl -X POST http://localhost:8080/api/v1/tickets \
  -H "Content-Type: application/json" \
  -d '{"order_id":"order-2001","pickup_slot":"15:00-15:15","queue_number":1}'
```

**GET ONE** (แทน `{id}` ด้วย id ที่ได้จาก POST หรือจาก seed data)
```bash
curl http://localhost:8080/api/v1/tickets/{id}
```

**PUT (เปลี่ยนสถานะ)**
```bash
curl -X PUT http://localhost:8080/api/v1/tickets/{id} \
  -H "Content-Type: application/json" \
  -d '{"status":"READY_FOR_PICKUP"}'
```

**DELETE**
```bash
curl -X DELETE http://localhost:8080/api/v1/tickets/{id}
```

หลังยิงแต่ละ method แนะนำเปิด `psql` หรือ pgAdmin คู่กันไว้ แล้ว `SELECT * FROM kitchen_tickets;`
เพื่อโชว์ว่าค่าใน DB เปลี่ยนตามจริงตามที่ assignment ต้องการ

## โครงสร้างโปรเจกต์

```
cmd/api/main.go          bootstrap: config, pgx pool, rabbitmq publisher, echo server
internal/config          โหลด .env
internal/models          struct ของ resource + request DTO
internal/repository      query กับ postgres ผ่าน pgx (Create/GetByID/GetAll/UpdateStatus/Delete)
internal/handler         echo handler แปลง HTTP <-> repository
internal/router          ผูก route กับ handler
internal/messaging       publish event ไปยัง RabbitMQ (optional, ไม่ทำให้ request ล้มถ้า broker ไม่พร้อม)
migrations/              SQL สร้างตาราง + seed
```
